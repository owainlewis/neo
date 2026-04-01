pub mod bash;
pub mod edit;
pub mod read;
pub mod write;

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

/// Callback for requesting tool approval from the user.
/// Returns true if approved, false if denied.
pub type ApprovalFn = Box<dyn Fn(&str, &str, &serde_json::Value) -> bool + Send + Sync>;

pub struct Registry {
    tools: Vec<Box<dyn Tool>>,
    approval_fn: Option<ApprovalFn>,
}

impl Registry {
    pub fn new(approval_fn: Option<ApprovalFn>) -> Self {
        Self {
            tools: vec![
                Box::new(bash::BashTool),
                Box::new(read::ReadTool),
                Box::new(edit::EditTool),
                Box::new(write::WriteTool),
            ],
            approval_fn,
        }
    }

    pub fn definitions(&self) -> Vec<ToolDefinition> {
        self.tools.iter().map(|t| t.definition()).collect()
    }

    pub fn get(&self, name: &str) -> Option<&dyn Tool> {
        self.tools.iter().find(|t| t.name() == name).map(|t| &**t)
    }

    /// Check if a tool use requires approval and if so, ask the user.
    /// Read-only tools are auto-approved.
    fn check_approval(&self, tool: &dyn Tool, tool_use: &ToolUseBlock) -> bool {
        if tool.is_read_only() {
            return true;
        }
        match &self.approval_fn {
            Some(f) => f(tool.name(), &tool_use.id, &tool_use.input),
            None => true, // No approval function = auto-approve everything
        }
    }

    /// Execute tool uses — read-only tools concurrently, write tools serially.
    /// Write tools require approval before execution.
    pub async fn execute_tools(&self, tool_uses: &[ToolUseBlock]) -> Vec<ToolResult> {
        let items = self.partition(tool_uses);
        let mut results = Vec::new();

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
                    let start = i;
                    while i < items.len() {
                        if let BatchItem::Resolved { tool, .. } = &items[i] {
                            if tool.is_read_only() {
                                i += 1;
                                continue;
                            }
                        }
                        break;
                    }
                    let futures: Vec<_> = items[start..i]
                        .iter()
                        .map(|item| async move {
                            let BatchItem::Resolved { tool_use, tool } = item else {
                                unreachable!()
                            };
                            match tool.execute(tool_use.input.clone()).await {
                                Ok(content) => ToolResult {
                                    tool_use_id: tool_use.id.clone(),
                                    content,
                                    is_error: false,
                                },
                                Err(e) => ToolResult {
                                    tool_use_id: tool_use.id.clone(),
                                    content: e,
                                    is_error: true,
                                },
                            }
                        })
                        .collect();
                    results.extend(futures::future::join_all(futures).await);
                }
                BatchItem::Resolved { tool_use, tool } => {
                    // Write tool — check approval first
                    if !self.check_approval(*tool, tool_use) {
                        results.push(ToolResult {
                            tool_use_id: tool_use.id.clone(),
                            content: "Tool use denied by user.".to_string(),
                            is_error: true,
                        });
                    } else {
                        let result = match tool.execute(tool_use.input.clone()).await {
                            Ok(content) => ToolResult {
                                tool_use_id: tool_use.id.clone(),
                                content,
                                is_error: false,
                            },
                            Err(e) => ToolResult {
                                tool_use_id: tool_use.id.clone(),
                                content: e,
                                is_error: true,
                            },
                        };
                        results.push(result);
                    }
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
    Resolved {
        tool_use: &'a ToolUseBlock,
        tool: &'a dyn Tool,
    },
    Unknown(&'a ToolUseBlock),
}
