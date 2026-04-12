use opus_core::{Tool, ToolOutput};
use serde_json::json;
use std::path::Path;

pub struct WriteTool;

#[async_trait::async_trait]
impl Tool for WriteTool {
    fn name(&self) -> &str {
        "write"
    }

    fn description(&self) -> &str {
        "Create or overwrite a file with the given content. Use for creating new files. For modifying existing files, prefer the edit tool."
    }

    fn input_schema(&self) -> serde_json::Value {
        json!({
            "type": "object",
            "properties": {
                "file_path": {
                    "type": "string",
                    "description": "Absolute path to the file to write"
                },
                "content": {
                    "type": "string",
                    "description": "The content to write to the file"
                }
            },
            "required": ["file_path", "content"]
        })
    }

    fn is_read_only(&self) -> bool {
        false
    }

    async fn execute(&self, input: serde_json::Value) -> Result<ToolOutput, String> {
        let file_path = input["file_path"]
            .as_str()
            .ok_or("Missing 'file_path' field")?;
        let content = input["content"]
            .as_str()
            .ok_or("Missing 'content' field")?;

        if let Some(parent) = Path::new(file_path).parent() {
            tokio::fs::create_dir_all(parent)
                .await
                .map_err(|e| format!("Failed to create directories: {}", e))?;
        }

        tokio::fs::write(file_path, content)
            .await
            .map_err(|e| format!("Failed to write {}: {}", file_path, e))?;

        let line_count = content.lines().count();
        Ok(ToolOutput::text(format!(
            "Wrote {} ({} lines)",
            file_path, line_count
        )))
    }
}
