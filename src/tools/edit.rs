use crate::tools::Tool;
use serde_json::json;

pub struct EditTool;

#[async_trait::async_trait]
impl Tool for EditTool {
    fn name(&self) -> &str {
        "edit"
    }

    fn description(&self) -> &str {
        "Edit a file by replacing an exact string match with new content. The old_string must match exactly (including whitespace and indentation). Use read first to see the file content."
    }

    fn input_schema(&self) -> serde_json::Value {
        json!({
            "type": "object",
            "properties": {
                "file_path": {
                    "type": "string",
                    "description": "Absolute path to the file to edit"
                },
                "old_string": {
                    "type": "string",
                    "description": "The exact string to find and replace"
                },
                "new_string": {
                    "type": "string",
                    "description": "The replacement string"
                }
            },
            "required": ["file_path", "old_string", "new_string"]
        })
    }

    fn is_read_only(&self) -> bool {
        false
    }

    async fn execute(&self, input: serde_json::Value) -> Result<String, String> {
        let file_path = input["file_path"]
            .as_str()
            .ok_or("Missing 'file_path' field")?;
        let old_string = input["old_string"]
            .as_str()
            .ok_or("Missing 'old_string' field")?;
        let new_string = input["new_string"]
            .as_str()
            .ok_or("Missing 'new_string' field")?;

        let content = tokio::fs::read_to_string(file_path)
            .await
            .map_err(|e| format!("Failed to read {}: {}", file_path, e))?;

        // Count occurrences
        let count = content.matches(old_string).count();

        if count == 0 {
            return Err(format!(
                "old_string not found in {}. Use the read tool first to verify the exact content.",
                file_path
            ));
        }

        if count > 1 {
            return Err(format!(
                "old_string found {} times in {}. Provide more context to make it unique.",
                count, file_path
            ));
        }

        let new_content = content.replacen(old_string, new_string, 1);

        tokio::fs::write(file_path, &new_content)
            .await
            .map_err(|e| format!("Failed to write {}: {}", file_path, e))?;

        Ok(format!("Successfully edited {}", file_path))
    }
}
