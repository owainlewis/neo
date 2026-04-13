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

    loop {
        turn_count += 1;
        if turn_count > state.max_turns {
            event_handler(&AgentEvent::Error("Max turns reached".into()));
            break;
        }

        // Hooks handle context management (compaction, steering, etc.)
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
        let result = truncate("café latte", 4);
        assert!(!result.is_empty());
    }
}
