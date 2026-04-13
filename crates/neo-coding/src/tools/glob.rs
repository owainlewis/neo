use neo_core::{Tool, ToolOutput};
use serde_json::json;

pub struct GlobTool;

#[async_trait::async_trait]
impl Tool for GlobTool {
    fn name(&self) -> &str {
        "glob"
    }

    fn description(&self) -> &str {
        "Find files matching a glob pattern. Returns matching file paths sorted alphabetically."
    }

    fn input_schema(&self) -> serde_json::Value {
        json!({
            "type": "object",
            "properties": {
                "pattern": {
                    "type": "string",
                    "description": "Glob pattern (e.g. '**/*.rs', 'src/**/*.ts')"
                },
                "path": {
                    "type": "string",
                    "description": "Base directory to search from (default: current working directory)"
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

        let base = input["path"].as_str().unwrap_or(".");

        let full_pattern = if pattern.starts_with('/') {
            pattern.to_string()
        } else {
            format!("{}/{}", base, pattern)
        };

        let entries = glob::glob(&full_pattern)
            .map_err(|e| format!("Invalid glob pattern: {}", e))?;

        let mut paths: Vec<String> = Vec::new();
        for entry in entries {
            match entry {
                Ok(path) => {
                    paths.push(path.display().to_string());
                    if paths.len() >= 200 {
                        break;
                    }
                }
                Err(e) => {
                    // Skip unreadable entries
                    eprintln!("glob error: {}", e);
                }
            }
        }

        paths.sort();

        if paths.is_empty() {
            return Ok(ToolOutput::text("No files matched.".to_string()));
        }

        let total = paths.len();
        let truncated = total >= 200;

        let mut result = format!("{} files matched", total);
        if truncated {
            result.push_str(" (limit reached, showing first 200)");
        }
        result.push('\n');

        for p in &paths {
            result.push_str(p);
            result.push('\n');
        }

        Ok(ToolOutput::text(result))
    }
}
