pub mod bash;
pub mod edit;
pub mod read;

use crate::model::types::{ToolDefinition, ToolResult, ToolUseBlock};

#[async_trait::async_trait]
pub trait Tool: Send + Sync {
    fn name(&self) -> &str;
    fn description(&self) -> &str;
    fn input_schema(&self) -> serde_json::Value;
    fn is_read_only(&self) -> bool;

    async fn execute(&self, input: serde_json::Value) -> Result<String, String>;

    fn definition(&self) -> ToolDefinition {
        ToolDefinition {
            name: self.name().to_string(),
            description: self.description().to_string(),
            input_schema: self.input_schema(),
        }
    }
}

pub struct Registry {
    tools: Vec<Box<dyn Tool>>,
}

impl Registry {
    pub fn new() -> Self {
        Self {
            tools: vec![
                Box::new(bash::BashTool),
                Box::new(read::ReadTool),
                Box::new(edit::EditTool),
            ],
        }
    }

    pub fn definitions(&self) -> Vec<ToolDefinition> {
        self.tools.iter().map(|t| t.definition()).collect()
    }

    pub fn get(&self, name: &str) -> Option<&dyn Tool> {
        self.tools.iter().find(|t| t.name() == name).map(|t| &**t)
    }

    /// Execute tool uses — read-only tools concurrently, write tools serially.
    pub async fn execute_tools(&self, tool_uses: &[ToolUseBlock]) -> Vec<ToolResult> {
        let items = self.partition(tool_uses);
        let mut results = Vec::new();

        // Group consecutive read-only items for concurrent execution
        let mut i = 0;
        while i < items.len() {
            match &items[i] {
                BatchItem::Unknown(tu) => {
                    results.push(ToolResult {
                        tool_use_id: tu.id.clone(),
                        content: format!("Unknown tool: '{}'", tu.name),
                        is_error: true,
                    });
                    i += 1;
                }
                BatchItem::Resolved { tool, .. } if tool.is_read_only() => {
                    // Collect consecutive read-only items
                    let start = i;
                    while i < items.len() {
                        if let BatchItem::Resolved { tool, .. } = &items[i] {
                            if tool.is_read_only() { i += 1; continue; }
                        }
                        break;
                    }
                    let futures: Vec<_> = items[start..i]
                        .iter()
                        .map(|item| async move {
                            let BatchItem::Resolved { tool_use, tool } = item else { unreachable!() };
                            match tool.execute(tool_use.input.clone()).await {
                                Ok(content) => ToolResult { tool_use_id: tool_use.id.clone(), content, is_error: false },
                                Err(e) => ToolResult { tool_use_id: tool_use.id.clone(), content: e, is_error: true },
                            }
                        })
                        .collect();
                    results.extend(futures::future::join_all(futures).await);
                }
                BatchItem::Resolved { tool_use, tool } => {
                    let result = match tool.execute(tool_use.input.clone()).await {
                        Ok(content) => ToolResult { tool_use_id: tool_use.id.clone(), content, is_error: false },
                        Err(e) => ToolResult { tool_use_id: tool_use.id.clone(), content: e, is_error: true },
                    };
                    results.push(result);
                    i += 1;
                }
            }
        }

        results
    }

    fn partition<'a>(&'a self, tool_uses: &'a [ToolUseBlock]) -> Vec<BatchItem<'a>> {
        let mut items: Vec<BatchItem> = Vec::new();

        for tu in tool_uses {
            match self.get(&tu.name) {
                Some(tool) => items.push(BatchItem::Resolved { tool_use: tu, tool }),
                None => items.push(BatchItem::Unknown(tu)),
            }
        }

        items
    }
}

enum BatchItem<'a> {
    Resolved { tool_use: &'a ToolUseBlock, tool: &'a dyn Tool },
    Unknown(&'a ToolUseBlock),
}
