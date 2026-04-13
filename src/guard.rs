use neo_core::{HookDecision, Hooks, ToolUseBlock};

/// Danger guard: blocks known-destructive bash commands. Everything else
/// runs without asking — the pi-mono philosophy. Layer stricter controls
/// via additional hooks if needed.
///
/// Only inspects `bash` tool calls. File operations (read/edit/write) are
/// always allowed — the model needs to write files to do its job.
pub struct DangerGuard;

/// Patterns that should never run without the user explicitly asking for them.
const BLOCKED_PATTERNS: &[&str] = &[
    // Destructive filesystem
    "rm -rf /",
    "rm -rf ~",
    "rm -rf $HOME",
    "rm -rf /*",
    "rm -rf ~/*",
    // Disk/partition
    "mkfs",
    "dd if=",
    // Force push to main branches
    "push --force origin main",
    "push --force origin master",
    "push -f origin main",
    "push -f origin master",
    // Hard resets
    "reset --hard",
    // Fork bomb
    ":(){ :|:&",
    // Permission nuking
    "chmod -R 777 /",
    "chmod -R 777 ~",
    // Credential exfiltration
    "curl.*ANTHROPIC_API_KEY",
    "curl.*API_KEY",
    "wget.*API_KEY",
];

#[async_trait::async_trait]
impl Hooks for DangerGuard {
    async fn before_tool_call(&self, call: &ToolUseBlock) -> HookDecision {
        if call.name != "bash" {
            return HookDecision::Allow;
        }

        let command = call.input["command"].as_str().unwrap_or("");

        for pattern in BLOCKED_PATTERNS {
            if command.contains(pattern) {
                return HookDecision::Block {
                    reason: format!(
                        "Blocked by danger guard: command matches '{}'. \
                         If you need to run this, ask the user to do it manually.",
                        pattern
                    ),
                };
            }
        }

        HookDecision::Allow
    }
}
