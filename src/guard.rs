use neo_core::{HookDecision, Hooks, ToolUseBlock};
use std::path::PathBuf;

/// Danger guard: blocks bash commands that could destroy your OS.
/// Everything else runs freely — git mistakes are recoverable, `rm -rf /` is not.
///
/// Configure in `~/.neo/config.toml`:
///
/// ```toml
/// [guard]
/// enabled = false            # same as --yolo
/// block = ["kubectl delete"] # add your own patterns
/// allow = ["dd if="]         # remove a built-in
/// ```
pub struct DangerGuard {
    patterns: Vec<String>,
}

/// Only things that could brick your machine or nuke your filesystem.
/// Git force-pushes, hard resets, etc. are recoverable — not blocked by default.
const BUILTINS: &[&str] = &[
    "rm -rf /",
    "rm -rf /*",
    "rm -rf ~",
    "rm -rf ~/*",
    "rm -rf $HOME",
    "mkfs",
    "dd if=",
    ":(){ :|:&",
    "chmod -R 777 /",
    "chmod -R 777 ~",
];

impl DangerGuard {
    /// Load from built-in defaults + `~/.neo/config.toml` [guard] section.
    pub fn load() -> Self {
        let mut patterns: Vec<String> = BUILTINS.iter().map(|s| s.to_string()).collect();
        let mut enabled = true;

        let config_path = std::env::var("HOME")
            .map(|h| PathBuf::from(h).join(".neo").join("config.toml"))
            .unwrap_or_default();

        if let Ok(content) = std::fs::read_to_string(&config_path) {
            let (block, allow, cfg_enabled) = parse_guard_section(&content);
            if let Some(e) = cfg_enabled {
                enabled = e;
            }
            patterns.extend(block);
            patterns.retain(|p| !allow.contains(p));
        }

        if !enabled {
            return Self::disabled();
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
        if call.name != "bash" || self.patterns.is_empty() {
            return HookDecision::Allow;
        }

        let command = call.input["command"].as_str().unwrap_or("");

        for pattern in &self.patterns {
            if command.contains(pattern.as_str()) {
                return HookDecision::Block {
                    reason: format!(
                        "Blocked: matches '{}'. Ask the user to run this manually.",
                        pattern
                    ),
                };
            }
        }

        HookDecision::Allow
    }
}

/// Parse [guard] section from config.toml.
fn parse_guard_section(content: &str) -> (Vec<String>, Vec<String>, Option<bool>) {
    let mut block = Vec::new();
    let mut allow = Vec::new();
    let mut enabled = None;
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

        // enabled = true/false
        if let Some(val) = line.strip_prefix("enabled") {
            let val = val.trim().trim_start_matches('=').trim();
            enabled = Some(val == "true");
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

    (block, allow, enabled)
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
    fn builtins_are_minimal() {
        let guard = DangerGuard::load();
        assert!(guard.patterns.contains(&"rm -rf /".to_string()));
        assert!(guard.patterns.contains(&"mkfs".to_string()));
        // Git operations should NOT be in defaults
        assert!(!guard.patterns.iter().any(|p| p.contains("push --force")));
        assert!(!guard.patterns.iter().any(|p| p.contains("reset --hard")));
    }

    #[test]
    fn disabled_has_no_patterns() {
        let guard = DangerGuard::disabled();
        assert!(guard.patterns.is_empty());
    }

    #[test]
    fn parse_guard_enabled_false() {
        let toml = "[guard]\nenabled = false\n";
        let (_, _, enabled) = parse_guard_section(toml);
        assert_eq!(enabled, Some(false));
    }

    #[test]
    fn parse_guard_block_and_allow() {
        let toml = r#"
[guard]
block = [
    "drop database",
]
allow = [
    "dd if=",
]
"#;
        let (block, allow, _) = parse_guard_section(toml);
        assert_eq!(block, vec!["drop database"]);
        assert_eq!(allow, vec!["dd if="]);
    }

    #[tokio::test]
    async fn blocks_rm_rf() {
        let guard = DangerGuard::load();
        let call = ToolUseBlock {
            id: "t1".into(),
            name: "bash".into(),
            input: serde_json::json!({"command": "rm -rf /"}),
        };
        assert!(matches!(
            guard.before_tool_call(&call).await,
            HookDecision::Block { .. }
        ));
    }

    #[tokio::test]
    async fn allows_git_force_push() {
        let guard = DangerGuard::load();
        let call = ToolUseBlock {
            id: "t1".into(),
            name: "bash".into(),
            input: serde_json::json!({"command": "git push --force origin main"}),
        };
        assert!(matches!(
            guard.before_tool_call(&call).await,
            HookDecision::Allow
        ));
    }

    #[tokio::test]
    async fn allows_normal_commands() {
        let guard = DangerGuard::load();
        let call = ToolUseBlock {
            id: "t1".into(),
            name: "bash".into(),
            input: serde_json::json!({"command": "cargo test --workspace"}),
        };
        assert!(matches!(
            guard.before_tool_call(&call).await,
            HookDecision::Allow
        ));
    }

    #[tokio::test]
    async fn disabled_allows_everything() {
        let guard = DangerGuard::disabled();
        let call = ToolUseBlock {
            id: "t1".into(),
            name: "bash".into(),
            input: serde_json::json!({"command": "rm -rf /"}),
        };
        assert!(matches!(
            guard.before_tool_call(&call).await,
            HookDecision::Allow
        ));
    }
}
