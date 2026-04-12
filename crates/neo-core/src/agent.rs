use crate::hooks::Hooks;
use crate::provider::Provider;
use crate::tool::Registry;
use crate::types::*;
use futures::StreamExt;
use std::collections::HashMap;

pub struct AgentState {
    messages: Vec<Message>,
    max_turns: usize,
    pub total_usage: Usage,
    system_prompt: String,
}

impl AgentState {
    pub fn new(max_turns: usize, system_prompt: String) -> Self {
        Self {
            messages: Vec::new(),
            max_turns,
            total_usage: Usage::default(),
            system_prompt,
        }
    }

    pub fn clear(&mut self) {
        self.messages.clear();
        self.total_usage = Usage::default();
    }

    pub fn add_user_message(&mut self, text: &str) {
        self.messages.push(Message::User {
            content: vec![ContentBlock::Text {
                text: text.to_string(),
            }],
        });
    }
}

/// Events emitted by the agent loop for the UI to render.
#[derive(Clone)]
pub enum AgentEvent {
    Thinking,
    ResponseReceived,
    Text(String),
    TextDelta(String),
    ToolComplete {
        name: String,
        input: String,
        result: String,
        is_error: bool,
        duration_ms: u64,
    },
    Done {
        usage: Usage,
    },
    Error(String),
    Info(String),
    Warning(String),
}

/// Run one turn of the agent loop: stream response, execute tools, repeat.
pub async fn run_turn(
    state: &mut AgentState,
    provider: &dyn Provider,
    registry: &Registry,
    hooks: &dyn Hooks,
    event_handler: &mut (dyn FnMut(&AgentEvent) + Send),
) {
    let mut turn_count = 0;
    // Only clear tool results from messages that existed *before* this
    // run_turn invocation. Tool results generated within this run (multi-turn
    // tool loops) are preserved so the model retains context mid-loop.
    let preserve_from = state.messages.len();

    loop {
        turn_count += 1;
        if turn_count > state.max_turns {
            event_handler(&AgentEvent::Error("Max turns reached".into()));
            break;
        }

        clear_stale_tool_results(&mut state.messages, preserve_from);
        hooks.transform_context(&mut state.messages).await;

        let system = hooks
            .augment_system_prompt(state.system_prompt.clone())
            .await;
        let tool_defs = hooks.filter_tools(registry.definitions()).await;

        event_handler(&AgentEvent::Thinking);

        // --- Stream the response ---
        let request = StreamRequest {
            system,
            messages: state.messages.clone(),
            tools: tool_defs,
        };
        let mut stream = provider.stream(request);

        let mut text_content = String::new();
        let mut tool_uses: Vec<ToolUseBlock> = Vec::new();
        let mut tool_json_buffers: HashMap<String, String> = HashMap::new();
        let mut usage = Usage::default();
        let mut response_signaled = false;

        while let Some(event) = stream.next().await {
            if !response_signaled {
                event_handler(&AgentEvent::ResponseReceived);
                response_signaled = true;
            }

            match event {
                ProviderEvent::TextDelta(delta) => {
                    text_content.push_str(&delta);
                    event_handler(&AgentEvent::TextDelta(delta));
                }
                ProviderEvent::ToolUseStart { id, name } => {
                    tool_json_buffers.insert(id.clone(), String::new());
                    tool_uses.push(ToolUseBlock {
                        id,
                        name,
                        input: serde_json::Value::Null,
                    });
                }
                ProviderEvent::ToolInputDelta { id, json_fragment } => {
                    if let Some(buf) = tool_json_buffers.get_mut(&id) {
                        buf.push_str(&json_fragment);
                    }
                }
                ProviderEvent::ToolUseEnd { id } => {
                    if let Some(json_str) = tool_json_buffers.remove(&id) {
                        if !json_str.is_empty() {
                            let input: serde_json::Value =
                                serde_json::from_str(&json_str).unwrap_or(serde_json::Value::Null);
                            if let Some(tu) = tool_uses.iter_mut().find(|t| t.id == id) {
                                tu.input = input;
                            }
                        }
                    }
                }
                ProviderEvent::Done { usage: u, .. } => {
                    usage = u;
                }
                ProviderEvent::Error(e) => {
                    event_handler(&AgentEvent::Error(e));
                    return;
                }
            }
        }

        state.total_usage.input_tokens += usage.input_tokens;
        state.total_usage.output_tokens += usage.output_tokens;

        // --- Build assistant message ---
        let mut assistant_content: Vec<ContentBlock> = Vec::new();
        if !text_content.is_empty() {
            assistant_content.push(ContentBlock::Text { text: text_content });
        }
        for tu in &tool_uses {
            assistant_content.push(ContentBlock::ToolUse {
                id: tu.id.clone(),
                name: tu.name.clone(),
                input: tu.input.clone(),
            });
        }

        state.messages.push(Message::Assistant {
            content: assistant_content,
        });

        // No tool calls → done
        if tool_uses.is_empty() {
            event_handler(&AgentEvent::Done {
                usage: state.total_usage.clone(),
            });
            break;
        }

        // --- Execute tools ---
        let start = std::time::Instant::now();
        let results = registry.execute_tools(&tool_uses, hooks).await;
        let elapsed_ms = start.elapsed().as_millis() as u64;

        // Accumulate token usage from tools that run subagents
        for result in &results {
            if let Some(ref u) = result.usage {
                state.total_usage.input_tokens += u.input_tokens;
                state.total_usage.output_tokens += u.output_tokens;
            }
        }

        let mut tool_results_content: Vec<ContentBlock> = Vec::new();
        for (i, result) in results.iter().enumerate() {
            let tu = tool_uses
                .iter()
                .find(|tu| tu.id == result.tool_use_id)
                .unwrap_or(&tool_uses[i]);

            event_handler(&AgentEvent::ToolComplete {
                name: tu.name.clone(),
                input: serde_json::to_string(&tu.input).unwrap_or_default(),
                result: truncate(&result.content, 500),
                is_error: result.is_error,
                duration_ms: elapsed_ms,
            });

            tool_results_content.push(ContentBlock::ToolResult {
                tool_use_id: result.tool_use_id.clone(),
                content: result.content.clone(),
                is_error: if result.is_error { Some(true) } else { None },
            });
        }

        state.messages.push(Message::User {
            content: tool_results_content,
        });
    }
}

/// Clear tool result content from messages that predate the current run_turn
/// invocation. Messages from `preserve_from` onward are left intact so the
/// model retains context across multi-turn tool loops within a single run.
fn clear_stale_tool_results(messages: &mut [Message], preserve_from: usize) {
    for message in messages[..preserve_from].iter_mut() {
        if let Message::User { content } = message {
            for block in content.iter_mut() {
                if let ContentBlock::ToolResult { content, .. } = block {
                    if *content != "[cleared]" {
                        *content = "[cleared]".to_string();
                    }
                }
            }
        }
    }
}

fn truncate(s: &str, max: usize) -> String {
    if s.len() <= max {
        s.to_string()
    } else {
        let mut end = max;
        while end > 0 && !s.is_char_boundary(end) {
            end -= 1;
        }
        format!("{}… ({} chars truncated)", &s[..end], s.len() - end)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tool_result_msg(id: &str, content: &str) -> Message {
        Message::User {
            content: vec![ContentBlock::ToolResult {
                tool_use_id: id.to_string(),
                content: content.to_string(),
                is_error: None,
            }],
        }
    }

    fn text_msg(text: &str) -> Message {
        Message::User {
            content: vec![ContentBlock::Text {
                text: text.to_string(),
            }],
        }
    }

    fn assistant_msg(text: &str) -> Message {
        Message::Assistant {
            content: vec![ContentBlock::Text {
                text: text.to_string(),
            }],
        }
    }

    fn get_tool_result_content(msg: &Message) -> Option<&str> {
        if let Message::User { content } = msg {
            for block in content {
                if let ContentBlock::ToolResult { content, .. } = block {
                    return Some(content);
                }
            }
        }
        None
    }

    #[test]
    fn clears_results_before_preserve_from() {
        let mut msgs = vec![
            text_msg("hello"),
            assistant_msg("response"),
            tool_result_msg("t1", "file contents here"),
            assistant_msg("thanks"),
            text_msg("next question"),
        ];

        clear_stale_tool_results(&mut msgs, 5);
        assert_eq!(get_tool_result_content(&msgs[2]), Some("[cleared]"));
    }

    #[test]
    fn preserves_results_after_preserve_from() {
        let mut msgs = vec![
            text_msg("old question"),
            assistant_msg("old response"),
            tool_result_msg("t1", "old result"),
            // --- preserve_from = 3 ---
            assistant_msg("new response"),
            tool_result_msg("t2", "new result"),
        ];

        clear_stale_tool_results(&mut msgs, 3);
        assert_eq!(get_tool_result_content(&msgs[2]), Some("[cleared]"));
        assert_eq!(get_tool_result_content(&msgs[4]), Some("new result"));
    }

    #[test]
    fn preserve_from_zero_clears_nothing() {
        let mut msgs = vec![
            tool_result_msg("t1", "keep me"),
            tool_result_msg("t2", "keep me too"),
        ];

        clear_stale_tool_results(&mut msgs, 0);
        assert_eq!(get_tool_result_content(&msgs[0]), Some("keep me"));
        assert_eq!(get_tool_result_content(&msgs[1]), Some("keep me too"));
    }

    #[test]
    fn already_cleared_not_double_cleared() {
        let mut msgs = vec![tool_result_msg("t1", "[cleared]")];
        clear_stale_tool_results(&mut msgs, 1);
        assert_eq!(get_tool_result_content(&msgs[0]), Some("[cleared]"));
    }

    #[test]
    fn assistant_messages_untouched() {
        let mut msgs = vec![assistant_msg("hello")];
        clear_stale_tool_results(&mut msgs, 1);
        if let Message::Assistant { content } = &msgs[0] {
            if let ContentBlock::Text { text } = &content[0] {
                assert_eq!(text, "hello");
            }
        }
    }

    #[test]
    fn truncate_short_string() {
        assert_eq!(truncate("hello", 10), "hello");
    }

    #[test]
    fn truncate_long_string() {
        let result = truncate("hello world", 5);
        assert!(result.starts_with("hello"));
        assert!(result.contains("truncated"));
    }

    #[test]
    fn truncate_unicode_boundary() {
        // 'é' is 2 bytes in UTF-8
        let result = truncate("café latte", 4);
        // Should not panic, and should truncate at a valid boundary
        assert!(result.len() > 0);
    }
}
