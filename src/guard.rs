use neo_core::{HookDecision, Hooks, ToolUseBlock};
use std::path::PathBuf;

/// Danger guard: blocks bash commands matching a pattern list.
/// Everything else runs without asking.
///
/// Patterns are **substring matches** — `"kubectl delete"` blocks any
/// command containing that string (e.g. `kubectl delete namespace prod`).
///
/// Configure in `~/.neo/config.toml`:
///
/// ```toml
/// [guard]
/// block = [
///     "drop database",
///     "kubectl delete",     # blocks kubectl delete anything
/// ]
/// allow = [
///     "reset --hard",       # remove a built-in you don't want
/// ]
/// ```
pub struct DangerGuard {
    patterns: Vec<String>,
}

const BUILTINS: &[&str] = &[
    "rm -rf /",
    "rm -rf ~",
    "rm -rf $HOME",
    "rm -rf /*",
    "rm -rf ~/*",
    "mkfs",
    "dd if=",
    "push --force origin main",
    "push --force origin master",
    "push -f origin main",
    "push -f origin master",
    "reset --hard",
    ":(){ :|:&",
    "chmod -R 777 /",
    "chmod -R 777 ~",
];

impl DangerGuard {
    pub fn load() -> Self {
        let mut patterns: Vec<String> = BUILTINS.iter().map(|s| s.to_string()).collect();

        // Read [guard] section from ~/.neo/config.toml
        let config_path = std::env::var("HOME")
            .map(|h| PathBuf::from(h).join(".neo").join("config.toml"))
            .unwrap_or_default();

        if let Ok(content) = std::fs::read_to_string(&config_path) {
            let (block, allow) = parse_guard_section(&content);
            patterns.extend(block);
            patterns.retain(|p| !allow.contains(p));
        }

        patterns.sort();
        patterns.dedup();
        Self { patterns }
    }

    pub fn disabled() -> Self {
        Self {
            patterns: Vec::new(),
        }
    }
}

#[async_trait::async_trait]
impl Hooks for DangerGuard {
    async fn before_tool_call(&self, call: &ToolUseBlock) -> HookDecision {
        if call.name != "bash" {
            return HookDecision::Allow;
        }

        let command = call.input["command"].as_str().unwrap_or("");

        for pattern in &self.patterns {
            if command.contains(pattern.as_str()) {
                return HookDecision::Block {
                    reason: format!(
                        "Blocked by danger guard: matches '{}'. \
                         Ask the user to run this manually.",
                        pattern
                    ),
                };
            }
        }

        HookDecision::Allow
    }
}

/// Parse [guard] block/allow arrays from a TOML config file.
fn parse_guard_section(content: &str) -> (Vec<String>, Vec<String>) {
    let mut block = Vec::new();
    let mut allow = Vec::new();
    let mut in_guard = false;
    let mut in_block = false;
    let mut in_allow = false;

    for line in content.lines() {
        let line = line.trim();

        if line == "[guard]" {
            in_guard = true;
            continue;
        }
        if line.starts_with('[') && line != "[guard]" {
            in_guard = false;
            in_block = false;
            in_allow = false;
            continue;
        }
        if !in_guard {
            continue;
        }

        if line.starts_with("block") {
            in_block = true;
            in_allow = false;
            continue;
        }
        if line.starts_with("allow") {
            in_allow = true;
            in_block = false;
            continue;
        }
        if line == "]" {
            in_block = false;
            in_allow = false;
            continue;
        }

        if let Some(val) = extract_quoted(line) {
            if in_block {
                block.push(val);
            } else if in_allow {
                allow.push(val);
            }
        }
    }

    (block, allow)
}

fn extract_quoted(line: &str) -> Option<String> {
    let line = line.trim().trim_end_matches(',');
    if (line.starts_with('"') && line.ends_with('"'))
        || (line.starts_with('\'') && line.ends_with('\''))
    {
        Some(line[1..line.len() - 1].to_string())
    } else {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn builtins_block_rm_rf() {
        let guard = DangerGuard::load();
        assert!(guard.patterns.contains(&"rm -rf /".to_string()));
    }

    #[test]
    fn disabled_has_no_patterns() {
        let guard = DangerGuard::disabled();
        assert!(guard.patterns.is_empty());
    }

    #[test]
    fn parse_guard_section_extracts_block_and_allow() {
        let toml = r#"
model = "claude-opus-4-6"

[guard]
block = [
    "drop database",
    "kubectl delete namespace",
]
allow = [
    "reset --hard",
]
"#;
        let (block, allow) = parse_guard_section(toml);
        assert_eq!(block, vec!["drop database", "kubectl delete namespace"]);
        assert_eq!(allow, vec!["reset --hard"]);
    }

    #[test]
    fn parse_guard_section_empty_config() {
        let (block, allow) = parse_guard_section("model = \"test\"\n");
        assert!(block.is_empty());
        assert!(allow.is_empty());
    }

    #[tokio::test]
    async fn blocks_dangerous_command() {
        let guard = DangerGuard::load();
        let call = ToolUseBlock {
            id: "t1".into(),
            name: "bash".into(),
            input: serde_json::json!({"command": "rm -rf /"}),
        };
        let decision = guard.before_tool_call(&call).await;
        assert!(matches!(decision, HookDecision::Block { .. }));
    }

    #[tokio::test]
    async fn allows_safe_command() {
        let guard = DangerGuard::load();
        let call = ToolUseBlock {
            id: "t1".into(),
            name: "bash".into(),
            input: serde_json::json!({"command": "ls -la"}),
        };
        let decision = guard.before_tool_call(&call).await;
        assert!(matches!(decision, HookDecision::Allow));
    }

    #[tokio::test]
    async fn allows_non_bash_tools() {
        let guard = DangerGuard::load();
        let call = ToolUseBlock {
            id: "t1".into(),
            name: "read".into(),
            input: serde_json::json!({"file_path": "/etc/passwd"}),
        };
        let decision = guard.before_tool_call(&call).await;
        assert!(matches!(decision, HookDecision::Allow));
    }

    #[tokio::test]
    async fn disabled_allows_everything() {
        let guard = DangerGuard::disabled();
        let call = ToolUseBlock {
            id: "t1".into(),
            name: "bash".into(),
            input: serde_json::json!({"command": "rm -rf /"}),
        };
        let decision = guard.before_tool_call(&call).await;
        assert!(matches!(decision, HookDecision::Allow));
    }
}
