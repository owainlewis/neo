use neo_core::{Tool, ToolOutput};
use serde_json::json;
use std::collections::HashSet;

pub struct GrepTool;

#[async_trait::async_trait]
impl Tool for GrepTool {
    fn name(&self) -> &str {
        "grep"
    }

    fn description(&self) -> &str {
        "Search file contents using regex patterns. Returns matching lines with file paths and line numbers."
    }

    fn input_schema(&self) -> serde_json::Value {
        json!({
            "type": "object",
            "properties": {
                "pattern": {
                    "type": "string",
                    "description": "Regex pattern to search for"
                },
                "path": {
                    "type": "string",
                    "description": "Directory or file to search in (default: current working directory)"
                },
                "include": {
                    "type": "string",
                    "description": "Glob filter for files (e.g. '*.rs', '*.py')"
                },
                "context_lines": {
                    "type": "integer",
                    "description": "Lines of context around matches (default: 0)"
                }
            },
            "required": ["pattern"]
        })
    }

    fn is_read_only(&self) -> bool {
        true
    }

    async fn execute(&self, input: serde_json::Value) -> Result<ToolOutput, String> {
        let pattern = input["pattern"]
            .as_str()
            .ok_or("Missing 'pattern' field")?;

        let path = input["path"]
            .as_str()
            .unwrap_or(".");

        let include = input["include"].as_str();
        let context_lines = input["context_lines"].as_u64().unwrap_or(0);

        let mut cmd = tokio::process::Command::new("grep");
        cmd.arg("-rn");

        if let Some(inc) = include {
            cmd.arg(format!("--include={}", inc));
        }

        if context_lines > 0 {
            cmd.arg(format!("-C{}", context_lines));
        }

        cmd.arg("-E");
        cmd.arg(pattern);
        cmd.arg(path);

        let output = cmd
            .output()
            .await
            .map_err(|e| format!("Failed to run grep: {}", e))?;

        let stdout = String::from_utf8_lossy(&output.stdout);

        if stdout.is_empty() {
            return Ok(ToolOutput::text("No matches found.".to_string()));
        }

        let lines: Vec<&str> = stdout.lines().collect();
        let total_matches = lines.len();

        // Count unique files
        let files: HashSet<&str> = lines
            .iter()
            .filter_map(|line| line.split(':').next())
            .collect();
        let file_count = files.len();

        let truncated = total_matches > 100;
        let display_lines = if truncated { &lines[..100] } else { &lines };

        let mut result = format!("{} matches in {} files", total_matches, file_count);
        if truncated {
            result.push_str(" (showing first 100)");
        }
        result.push('\n');

        for line in display_lines {
            result.push_str(line);
            result.push('\n');
        }

        Ok(ToolOutput::text(result))
    }
}
