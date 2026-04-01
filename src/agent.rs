use crate::model::types::*;
use crate::model::Provider;
use crate::prompt;
use crate::tools::Registry;
use futures::StreamExt;

pub struct AgentState {
    messages: Vec<Message>,
    max_turns: usize,
    pub total_usage: Usage,
    system_prompt: String,
}

impl AgentState {
    pub fn new(max_turns: usize) -> Self {
        let cwd = std::env::current_dir()
            .map(|p| p.to_string_lossy().to_string())
            .unwrap_or_else(|_| ".".to_string());

        Self {
            messages: Vec::new(),
            max_turns,
            total_usage: Usage::default(),
            system_prompt: prompt::build_system_prompt(&cwd),
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
pub enum AgentEvent {
    Text(String),
    ToolComplete {
        name: String,
        input: String,
        result: String,
        is_error: bool,
        duration_ms: u64,
    },
    Done { usage: Usage },
    Error(String),
}

/// Run one turn of the agent loop: stream response, execute tools, repeat until done.
pub async fn run_turn(
    state: &mut AgentState,
    provider: &dyn Provider,
    registry: &Registry,
    event_handler: &mut dyn FnMut(&AgentEvent),
) {
    let tool_defs = registry.definitions();
    let mut turn_count = 0;

    loop {
        turn_count += 1;
        if turn_count > state.max_turns {
            event_handler(&AgentEvent::Error("Max turns reached".into()));
            break;
        }

        // Stream model response
        let mut stream = provider
            .stream(&state.system_prompt, &state.messages, &tool_defs)
            .await;

        let mut text_content = String::new();
        let mut tool_uses: Vec<ToolUseBlock> = Vec::new();

        while let Some(event) = stream.next().await {
            match event {
                StreamEvent::Text(t) => {
                    text_content.push_str(&t);
                }
                StreamEvent::ToolUse(tu) => {
                    tool_uses.push(tu);
                }
                StreamEvent::Done(usage) => {
                    state.total_usage.input_tokens += usage.input_tokens;
                    state.total_usage.output_tokens += usage.output_tokens;
                }
                StreamEvent::Error(e) => {
                    event_handler(&AgentEvent::Error(e));
                    return;
                }
            }
        }

        // Emit buffered text as a single block
        if !text_content.is_empty() {
            event_handler(&AgentEvent::Text(text_content.clone()));
        }

        // Build assistant message content
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

        // Execute tools and emit combined events
        let start = std::time::Instant::now();
        let results = registry.execute_tools(&tool_uses).await;
        let elapsed_ms = start.elapsed().as_millis() as u64;

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
        format!("{}… ({} chars truncated)", &s[..max], s.len() - max)
    }
}
