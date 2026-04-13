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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::hooks::NoHooks;
    use crate::types::{ToolOutput, Usage};

    // Mock read-only tool that returns its name
    struct MockReadTool;

    #[async_trait::async_trait]
    impl Tool for MockReadTool {
        fn name(&self) -> &str { "mock_read" }
        fn description(&self) -> &str { "mock" }
        fn input_schema(&self) -> serde_json::Value { serde_json::json!({}) }
        fn is_read_only(&self) -> bool { true }
        async fn execute(&self, _: serde_json::Value) -> Result<ToolOutput, String> {
            Ok(ToolOutput::text("read_result".into()))
        }
    }

    // Mock write tool
    struct MockWriteTool;

    #[async_trait::async_trait]
    impl Tool for MockWriteTool {
        fn name(&self) -> &str { "mock_write" }
        fn description(&self) -> &str { "mock" }
        fn input_schema(&self) -> serde_json::Value { serde_json::json!({}) }
        fn is_read_only(&self) -> bool { false }
        async fn execute(&self, _: serde_json::Value) -> Result<ToolOutput, String> {
            Ok(ToolOutput::text("write_result".into()))
        }
    }

    // Mock tool that returns usage
    struct MockUsageTool;

    #[async_trait::async_trait]
    impl Tool for MockUsageTool {
        fn name(&self) -> &str { "mock_usage" }
        fn description(&self) -> &str { "mock" }
        fn input_schema(&self) -> serde_json::Value { serde_json::json!({}) }
        fn is_read_only(&self) -> bool { false }
        async fn execute(&self, _: serde_json::Value) -> Result<ToolOutput, String> {
            Ok(ToolOutput::with_usage("done".into(), Usage { input_tokens: 100, output_tokens: 50 }))
        }
    }

    // Mock tool that always errors
    struct MockErrorTool;

    #[async_trait::async_trait]
    impl Tool for MockErrorTool {
        fn name(&self) -> &str { "mock_error" }
        fn description(&self) -> &str { "mock" }
        fn input_schema(&self) -> serde_json::Value { serde_json::json!({}) }
        fn is_read_only(&self) -> bool { true }
        async fn execute(&self, _: serde_json::Value) -> Result<ToolOutput, String> {
            Err("something went wrong".into())
        }
    }

    fn tool_use(id: &str, name: &str) -> ToolUseBlock {
        ToolUseBlock { id: id.into(), name: name.into(), input: serde_json::json!({}) }
    }

    #[test]
    fn definitions_returns_all_tools() {
        let registry = Registry::new(vec![
            Box::new(MockReadTool),
            Box::new(MockWriteTool),
        ]);
        let defs = registry.definitions();
        assert_eq!(defs.len(), 2);
        assert!(defs[0].read_only);
        assert!(!defs[1].read_only);
    }

    #[test]
    fn write_tool_names_filters_correctly() {
        let registry = Registry::new(vec![
            Box::new(MockReadTool),
            Box::new(MockWriteTool),
        ]);
        let names = registry.write_tool_names();
        assert_eq!(names, vec!["mock_write"]);
    }

    #[test]
    fn get_finds_tool_by_name() {
        let registry = Registry::new(vec![Box::new(MockReadTool)]);
        assert!(registry.get("mock_read").is_some());
        assert!(registry.get("nonexistent").is_none());
    }

    #[tokio::test]
    async fn execute_unknown_tool_returns_error() {
        let registry = Registry::new(vec![Box::new(MockReadTool)]);
        let uses = vec![tool_use("t1", "nonexistent")];
        let results = registry.execute_tools(&uses, &NoHooks).await;
        assert_eq!(results.len(), 1);
        assert!(results[0].is_error);
        assert!(results[0].content.contains("Unknown tool"));
    }

    #[tokio::test]
    async fn execute_read_tool_succeeds() {
        let registry = Registry::new(vec![Box::new(MockReadTool)]);
        let uses = vec![tool_use("t1", "mock_read")];
        let results = registry.execute_tools(&uses, &NoHooks).await;
        assert_eq!(results.len(), 1);
        assert!(!results[0].is_error);
        assert_eq!(results[0].content, "read_result");
        assert!(results[0].usage.is_none());
    }

    #[tokio::test]
    async fn execute_tool_with_usage_propagates() {
        let registry = Registry::new(vec![Box::new(MockUsageTool)]);
        let uses = vec![tool_use("t1", "mock_usage")];
        let results = registry.execute_tools(&uses, &NoHooks).await;
        assert!(!results[0].is_error);
        let usage = results[0].usage.as_ref().unwrap();
        assert_eq!(usage.input_tokens, 100);
        assert_eq!(usage.output_tokens, 50);
    }

    #[tokio::test]
    async fn execute_error_tool_returns_error() {
        let registry = Registry::new(vec![Box::new(MockErrorTool)]);
        let uses = vec![tool_use("t1", "mock_error")];
        let results = registry.execute_tools(&uses, &NoHooks).await;
        assert!(results[0].is_error);
        assert_eq!(results[0].content, "something went wrong");
    }

    #[tokio::test]
    async fn execute_multiple_read_tools_concurrently() {
        let registry = Registry::new(vec![Box::new(MockReadTool)]);
        let uses = vec![
            tool_use("t1", "mock_read"),
            tool_use("t2", "mock_read"),
            tool_use("t3", "mock_read"),
        ];
        let results = registry.execute_tools(&uses, &NoHooks).await;
        assert_eq!(results.len(), 3);
        assert!(results.iter().all(|r| !r.is_error));
    }

    #[tokio::test]
    async fn blocked_tool_returns_error_result() {
        use crate::hooks::HookChain;
        use std::sync::Arc;

        struct BlockBash;

        #[async_trait::async_trait]
        impl crate::hooks::Hooks for BlockBash {
            async fn before_tool_call(&self, call: &ToolUseBlock) -> HookDecision {
                if call.name == "mock_write" {
                    HookDecision::Block { reason: "denied".into() }
                } else {
                    HookDecision::Allow
                }
            }
        }

        let registry = Registry::new(vec![Box::new(MockWriteTool)]);
        let hooks = HookChain::new().add(Arc::new(BlockBash));
        let uses = vec![tool_use("t1", "mock_write")];
        let results = registry.execute_tools(&uses, &hooks).await;
        assert!(results[0].is_error);
        assert_eq!(results[0].content, "denied");
    }
}
