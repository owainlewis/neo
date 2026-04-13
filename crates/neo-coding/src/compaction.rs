use neo_core::{ContentBlock, Hooks, Message, Usage};
use std::sync::Mutex;

/// Context compaction hook. Watches token usage and replaces old messages
/// with a structured summary when approaching the context window limit.
///
/// Strategy:
/// 1. Track token count from the last API response
/// 2. When `input_tokens > threshold`, find a cut point in the message history
/// 3. Summarize everything before the cut point into a compact checkpoint
/// 4. Replace those messages with a single summary message
///
/// The summary uses a fixed format so the model sees consistent context on resume:
/// ```text
/// [Context checkpoint]
/// Goal: <what the user asked for>
/// Progress: <what's been done>
/// Key files: <files read or modified>
/// Next: <what was about to happen>
/// ```
pub struct CompactionHook {
    /// Compact when input tokens exceed this threshold (default: 80% of context window)
    threshold: u32,
    /// Last known usage from the provider
    last_usage: Mutex<Usage>,
}

impl CompactionHook {
    /// Create with a context window size. Compaction triggers at 80% capacity.
    pub fn new(context_window: u32) -> Self {
        Self {
            threshold: (context_window as f64 * 0.8) as u32,
            last_usage: Mutex::new(Usage::default()),
        }
    }

    /// Update with the latest usage from the provider response.
    pub fn update_usage(&self, usage: &Usage) {
        if let Ok(mut last) = self.last_usage.lock() {
            *last = usage.clone();
        }
    }

    fn should_compact(&self) -> bool {
        self.last_usage
            .lock()
            .map(|u| u.input_tokens > self.threshold)
            .unwrap_or(false)
    }
}

#[async_trait::async_trait]
impl Hooks for CompactionHook {
    async fn transform_context(&self, messages: &mut Vec<Message>) {
        if !self.should_compact() || messages.len() < 4 {
            return;
        }

        // Keep the last 4 messages (recent context the model needs)
        let keep_recent = 4.min(messages.len());
        let cut_point = messages.len() - keep_recent;

        if cut_point < 2 {
            return;
        }

        // Build summary from old messages
        let summary = summarize_messages(&messages[..cut_point]);

        // Replace old messages with a single summary message
        let recent: Vec<Message> = messages[cut_point..].to_vec();
        messages.clear();

        // Insert summary as a user message so the model has context
        messages.push(Message::User {
            content: vec![ContentBlock::Text {
                text: summary,
            }],
        });

        // Append a synthetic assistant acknowledgment so message
        // alternation (user/assistant/user/...) stays valid
        messages.push(Message::Assistant {
            content: vec![ContentBlock::Text {
                text: "Understood. I have the context from the checkpoint above. Continuing."
                    .to_string(),
            }],
        });

        messages.extend(recent);
    }
}

/// Build a structured checkpoint summary from a slice of messages.
fn summarize_messages(messages: &[Message]) -> String {
    let mut goal = String::new();
    let mut actions = Vec::new();
    let mut files = Vec::new();
    let mut errors = Vec::new();

    for msg in messages {
        match msg {
            Message::User { content } => {
                for block in content {
                    match block {
                        ContentBlock::Text { text } => {
                            // First user text message is likely the goal
                            if goal.is_empty() && !text.starts_with("[Context checkpoint]") {
                                goal = truncate(text, 200);
                            }
                        }
                        ContentBlock::ToolResult {
                            content, is_error, ..
                        } => {
                            if is_error == &Some(true) {
                                errors.push(truncate(content, 100));
                            }
                        }
                        _ => {}
                    }
                }
            }
            Message::Assistant { content } => {
                for block in content {
                    match block {
                        ContentBlock::Text { text } => {
                            // Extract key action sentences (first line of each text block)
                            if let Some(first_line) = text.lines().next() {
                                let trimmed = first_line.trim();
                                if !trimmed.is_empty() && trimmed.len() > 10 {
                                    actions.push(truncate(trimmed, 150));
                                }
                            }
                        }
                        ContentBlock::ToolUse { name, input, .. } => {
                            // Track file operations
                            if let Some(path) = input.get("file_path").and_then(|v| v.as_str()) {
                                let short = shorten_path(path);
                                let entry = format!("{}: {}", name, short);
                                if !files.contains(&entry) {
                                    files.push(entry);
                                }
                            }
                            if name == "bash" {
                                if let Some(cmd) = input.get("command").and_then(|v| v.as_str()) {
                                    actions.push(format!("$ {}", truncate(cmd, 80)));
                                }
                            }
                        }
                        _ => {}
                    }
                }
            }
        }
    }

    let mut summary = String::from("[Context checkpoint]\n");

    summary.push_str(&format!("Goal: {}\n", if goal.is_empty() { "(unknown)" } else { &goal }));

    if !actions.is_empty() {
        summary.push_str("Progress:\n");
        // Show last N actions to keep it compact
        let start = actions.len().saturating_sub(8);
        for action in &actions[start..] {
            summary.push_str(&format!("- {}\n", action));
        }
    }

    if !files.is_empty() {
        summary.push_str("Files touched:\n");
        let start = files.len().saturating_sub(15);
        for f in &files[start..] {
            summary.push_str(&format!("- {}\n", f));
        }
    }

    if !errors.is_empty() {
        summary.push_str("Errors encountered:\n");
        for e in errors.iter().take(3) {
            summary.push_str(&format!("- {}\n", e));
        }
    }

    summary
}

fn truncate(s: &str, max: usize) -> String {
    if s.len() <= max {
        s.to_string()
    } else {
        let mut end = max;
        while end > 0 && !s.is_char_boundary(end) {
            end -= 1;
        }
        format!("{}...", &s[..end])
    }
}

fn shorten_path(path: &str) -> String {
    let home = std::env::var("HOME").unwrap_or_default();
    let path = if !home.is_empty() && path.starts_with(&home) {
        format!("~{}", &path[home.len()..])
    } else {
        path.to_string()
    };
    let parts: Vec<&str> = path.split('/').collect();
    if parts.len() <= 4 {
        path
    } else {
        format!(".../{}", parts[parts.len() - 3..].join("/"))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn user_text(text: &str) -> Message {
        Message::User {
            content: vec![ContentBlock::Text { text: text.to_string() }],
        }
    }

    fn assistant_text(text: &str) -> Message {
        Message::Assistant {
            content: vec![ContentBlock::Text { text: text.to_string() }],
        }
    }

    fn tool_use_msg(name: &str, file_path: &str) -> Message {
        Message::Assistant {
            content: vec![ContentBlock::ToolUse {
                id: "t1".into(),
                name: name.to_string(),
                input: serde_json::json!({"file_path": file_path}),
            }],
        }
    }

    fn tool_result(id: &str, content: &str) -> Message {
        Message::User {
            content: vec![ContentBlock::ToolResult {
                tool_use_id: id.to_string(),
                content: content.to_string(),
                is_error: None,
            }],
        }
    }

    #[test]
    fn summarize_extracts_goal() {
        let msgs = vec![
            user_text("Fix the login bug in auth.rs"),
            assistant_text("I'll look at the auth module."),
        ];
        let summary = summarize_messages(&msgs);
        assert!(summary.contains("Fix the login bug"));
    }

    #[test]
    fn summarize_tracks_files() {
        let msgs = vec![
            user_text("Fix it"),
            tool_use_msg("read", "/src/auth.rs"),
            tool_result("t1", "file contents"),
            tool_use_msg("edit", "/src/auth.rs"),
            tool_result("t1", "ok"),
        ];
        let summary = summarize_messages(&msgs);
        assert!(summary.contains("auth.rs"));
    }

    #[tokio::test]
    async fn no_compaction_below_threshold() {
        let hook = CompactionHook::new(100_000);
        // Usage well below threshold
        hook.update_usage(&Usage { input_tokens: 1000, output_tokens: 100 });

        let mut msgs = vec![
            user_text("hello"),
            assistant_text("hi"),
            user_text("do something"),
            assistant_text("done"),
        ];
        let original_len = msgs.len();
        hook.transform_context(&mut msgs).await;
        assert_eq!(msgs.len(), original_len); // no change
    }

    #[tokio::test]
    async fn compaction_triggered_above_threshold() {
        let hook = CompactionHook::new(1000); // very low threshold
        hook.update_usage(&Usage { input_tokens: 900, output_tokens: 100 });

        let mut msgs = vec![
            user_text("Fix the bug"),
            assistant_text("Looking at it"),
            user_text("Any progress?"),
            assistant_text("Found the issue"),
            user_text("Great, fix it"),
            assistant_text("Done"),
            user_text("Thanks"),
            assistant_text("You're welcome"),
        ];

        hook.transform_context(&mut msgs).await;

        // Should be compacted: summary + ack + last 4 messages = 6
        assert!(msgs.len() < 8);
        // First message should be the checkpoint
        if let Message::User { content } = &msgs[0] {
            if let ContentBlock::Text { text } = &content[0] {
                assert!(text.contains("[Context checkpoint]"));
                assert!(text.contains("Fix the bug"));
            }
        }
    }
}
