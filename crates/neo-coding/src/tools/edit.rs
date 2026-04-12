use neo_core::{Tool, ToolOutput};
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

    async fn execute(&self, input: serde_json::Value) -> Result<ToolOutput, String> {
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

        Ok(ToolOutput::text(format!("Successfully edited {}", file_path)))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;
    use tempfile::NamedTempFile;

    async fn run_edit(content: &str, old: &str, new: &str) -> Result<ToolOutput, String> {
        let mut f = NamedTempFile::new().unwrap();
        write!(f, "{}", content).unwrap();
        let path = f.path().to_str().unwrap().to_string();

        EditTool
            .execute(serde_json::json!({
                "file_path": path,
                "old_string": old,
                "new_string": new,
            }))
            .await
    }

    #[tokio::test]
    async fn successful_edit() {
        let result = run_edit("hello world", "world", "rust").await;
        assert!(result.is_ok());
        assert!(result.unwrap().content.contains("Successfully edited"));
    }

    #[tokio::test]
    async fn old_string_not_found() {
        let result = run_edit("hello world", "missing", "rust").await;
        assert!(result.is_err());
        assert!(result.unwrap_err().contains("not found"));
    }

    #[tokio::test]
    async fn old_string_not_unique() {
        let result = run_edit("aaa bbb aaa", "aaa", "ccc").await;
        assert!(result.is_err());
        assert!(result.unwrap_err().contains("2 times"));
    }

    #[tokio::test]
    async fn edit_preserves_surrounding_content() {
        let mut f = NamedTempFile::new().unwrap();
        write!(f, "line1\nline2\nline3").unwrap();
        let path = f.path().to_str().unwrap().to_string();

        let _ = EditTool
            .execute(serde_json::json!({
                "file_path": path,
                "old_string": "line2",
                "new_string": "replaced",
            }))
            .await
            .unwrap();

        let content = std::fs::read_to_string(&path).unwrap();
        assert_eq!(content, "line1\nreplaced\nline3");
    }

    #[tokio::test]
    async fn nonexistent_file() {
        let result = EditTool
            .execute(serde_json::json!({
                "file_path": "/tmp/neo_test_nonexistent_file_12345",
                "old_string": "x",
                "new_string": "y",
            }))
            .await;
        assert!(result.is_err());
        assert!(result.unwrap_err().contains("Failed to read"));
    }
}
