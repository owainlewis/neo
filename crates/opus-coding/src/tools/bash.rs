use opus_core::{Tool, ToolOutput};
use serde_json::json;
use tokio::process::Command;

pub struct BashTool;

#[async_trait::async_trait]
impl Tool for BashTool {
    fn name(&self) -> &str {
        "bash"
    }

    fn description(&self) -> &str {
        "Execute a bash command and return its output. Use for running shell commands, installing packages, running tests, git operations, etc."
    }

    fn input_schema(&self) -> serde_json::Value {
        json!({
            "type": "object",
            "properties": {
                "command": {
                    "type": "string",
                    "description": "The bash command to execute"
                },
                "timeout_ms": {
                    "type": "integer",
                    "description": "Optional timeout in milliseconds (default: 120000)"
                }
            },
            "required": ["command"]
        })
    }

    fn is_read_only(&self) -> bool {
        false
    }

    async fn execute(&self, input: serde_json::Value) -> Result<ToolOutput, String> {
        let command = input["command"]
            .as_str()
            .ok_or("Missing 'command' field")?;

        let timeout_ms = input["timeout_ms"].as_u64().unwrap_or(120_000);

        let result = tokio::time::timeout(
            std::time::Duration::from_millis(timeout_ms),
            Command::new("bash").arg("-c").arg(command).output(),
        )
        .await;

        match result {
            Ok(Ok(output)) => {
                let stdout = String::from_utf8_lossy(&output.stdout);
                let stderr = String::from_utf8_lossy(&output.stderr);

                let mut result = String::new();
                if !stdout.is_empty() {
                    result.push_str(&stdout);
                }
                if !stderr.is_empty() {
                    if !result.is_empty() {
                        result.push('\n');
                    }
                    result.push_str("STDERR:\n");
                    result.push_str(&stderr);
                }

                if output.status.success() {
                    Ok(ToolOutput::text(if result.is_empty() {
                        "(no output)".to_string()
                    } else {
                        result
                    }))
                } else {
                    Err(format!(
                        "Exit code {}\n{}",
                        output.status.code().unwrap_or(-1),
                        result
                    ))
                }
            }
            Ok(Err(e)) => Err(format!("Failed to execute: {}", e)),
            Err(_) => Err(format!("Command timed out after {}ms", timeout_ms)),
        }
    }
}
