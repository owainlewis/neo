use crate::hooks::{HookDecision, Hooks};
use crate::types::{ToolDefinition, ToolOutput, ToolResult, ToolUseBlock};

#[async_trait::async_trait]
pub trait Tool: Send + Sync {
    fn name(&self) -> &str;
    fn description(&self) -> &str;
    fn input_schema(&self) -> serde_json::Value;
    fn is_read_only(&self) -> bool;

    async fn execute(&self, input: serde_json::Value) -> Result<ToolOutput, String>;

    fn definition(&self) -> ToolDefinition {
        ToolDefinition {
            name: self.name().to_string(),
            description: self.description().to_string(),
            input_schema: self.input_schema(),
            read_only: self.is_read_only(),
        }
    }
}

/// Deprecated alias retained for older call sites. New code should use `Hooks`.
pub type ApprovalFn = Box<dyn Fn(&str, &str, &serde_json::Value) -> bool + Send + Sync>;

pub struct Registry {
    tools: Vec<Box<dyn Tool>>,
}

impl Registry {
    /// Construct a registry from caller-supplied tools. The core never
    /// hardcodes a tool set — bundles (e.g. `neo-coding`) provide them.
    pub fn new(tools: Vec<Box<dyn Tool>>) -> Self {
        Self { tools }
    }

    /// All tool definitions. Hooks (e.g. `PlanModeHook`) filter this downstream.
    pub fn definitions(&self) -> Vec<ToolDefinition> {
        self.tools.iter().map(|t| t.definition()).collect()
    }

    pub fn get(&self, name: &str) -> Option<&dyn Tool> {
        self.tools.iter().find(|t| t.name() == name).map(|t| &**t)
    }

    /// Names of tools that are not read-only. Useful for binaries that want to
    /// build an approval hook scoped to write tools.
    pub fn write_tool_names(&self) -> Vec<String> {
        self.tools
            .iter()
            .filter(|t| !t.is_read_only())
            .map(|t| t.name().to_string())
            .collect()
    }

    /// Execute tool uses. Read-only tools run concurrently; writes run
    /// serially. Each tool call passes through `hooks.before_tool_call`
    /// (which may block it) and `hooks.after_tool_call` (which may rewrite
    /// the result).
    pub async fn execute_tools(
        &self,
        tool_uses: &[ToolUseBlock],
        hooks: &dyn Hooks,
    ) -> Vec<ToolResult> {
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
                        usage: None,
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
                        .map(|item| {
                            let BatchItem::Resolved { tool_use, tool } = item else {
                                unreachable!()
                            };
                            run_one(tool_use, *tool, hooks)
                        })
                        .collect();
                    results.extend(futures::future::join_all(futures).await);
                }
                BatchItem::Resolved { tool_use, tool } => {
                    results.push(run_one(tool_use, *tool, hooks).await);
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

async fn run_one(tool_use: &ToolUseBlock, tool: &dyn Tool, hooks: &dyn Hooks) -> ToolResult {
    let result = match hooks.before_tool_call(tool_use).await {
        HookDecision::Block { reason } => ToolResult {
            tool_use_id: tool_use.id.clone(),
            content: reason,
            is_error: true,
            usage: None,
        },
        HookDecision::Allow => match tool.execute(tool_use.input.clone()).await {
            Ok(output) => ToolResult {
                tool_use_id: tool_use.id.clone(),
                content: output.content,
                is_error: false,
                usage: output.usage,
            },
            Err(e) => ToolResult {
                tool_use_id: tool_use.id.clone(),
                content: e,
                is_error: true,
                usage: None,
            },
        },
    };
    hooks.after_tool_call(tool_use, result).await
}

enum BatchItem<'a> {
    Resolved {
        tool_use: &'a ToolUseBlock,
        tool: &'a dyn Tool,
    },
    Unknown(&'a ToolUseBlock),
}
