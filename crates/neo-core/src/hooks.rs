use crate::types::{Message, ToolDefinition, ToolResult, ToolUseBlock};
use std::sync::Arc;

/// Decision returned by `Hooks::before_tool_call`.
pub enum HookDecision {
    /// Allow the tool call to execute.
    Allow,
    /// Block the tool call. The reason is surfaced to the model as a
    /// synthetic error tool result.
    Block { reason: String },
}

/// Hooks are the extension surface for the agent loop. Every method has a
/// safe default — implementors only override what they care about.
///
/// Hooks replace what other coding agents bake into the core loop as "plan
/// mode", "approval system", "context compaction", etc. Compose multiple
/// hooks with `HookChain`.
#[async_trait::async_trait]
pub trait Hooks: Send + Sync {
    /// Modify the system prompt before each turn. Chain-friendly: later hooks
    /// see earlier hooks' output.
    async fn augment_system_prompt(&self, prompt: String) -> String {
        prompt
    }

    /// Filter or reorder tool definitions before they're sent to the provider.
    /// Use this to hide tools conditionally (e.g. plan mode hides writes).
    async fn filter_tools(&self, tools: Vec<ToolDefinition>) -> Vec<ToolDefinition> {
        tools
    }

    /// Inspect or rewrite the message history before it's sent to the provider.
    /// Use this for compaction, steering injection, etc.
    async fn transform_context(&self, _messages: &mut Vec<Message>) {}

    /// Called before each tool invocation. Return `Block` to prevent execution
    /// (the reason becomes an error tool result sent back to the model).
    async fn before_tool_call(&self, _call: &ToolUseBlock) -> HookDecision {
        HookDecision::Allow
    }

    /// Called after each tool result. Use this to rewrite, truncate, or
    /// annotate results before they reach the model.
    async fn after_tool_call(&self, _call: &ToolUseBlock, result: ToolResult) -> ToolResult {
        result
    }
}

/// A no-op hook. Useful as a default when no extensions are installed.
pub struct NoHooks;

#[async_trait::async_trait]
impl Hooks for NoHooks {}

/// Compose multiple hooks into one. Hooks run in registration order;
/// `before_tool_call` short-circuits on the first `Block`.
#[derive(Default)]
pub struct HookChain {
    hooks: Vec<Arc<dyn Hooks>>,
}

impl HookChain {
    pub fn new() -> Self {
        Self { hooks: Vec::new() }
    }

    pub fn add(mut self, hook: Arc<dyn Hooks>) -> Self {
        self.hooks.push(hook);
        self
    }
}

#[async_trait::async_trait]
impl Hooks for HookChain {
    async fn augment_system_prompt(&self, mut prompt: String) -> String {
        for h in &self.hooks {
            prompt = h.augment_system_prompt(prompt).await;
        }
        prompt
    }

    async fn filter_tools(&self, mut tools: Vec<ToolDefinition>) -> Vec<ToolDefinition> {
        for h in &self.hooks {
            tools = h.filter_tools(tools).await;
        }
        tools
    }

    async fn transform_context(&self, messages: &mut Vec<Message>) {
        for h in &self.hooks {
            h.transform_context(messages).await;
        }
    }

    async fn before_tool_call(&self, call: &ToolUseBlock) -> HookDecision {
        for h in &self.hooks {
            match h.before_tool_call(call).await {
                HookDecision::Allow => continue,
                block => return block,
            }
        }
        HookDecision::Allow
    }

    async fn after_tool_call(&self, call: &ToolUseBlock, mut result: ToolResult) -> ToolResult {
        for h in &self.hooks {
            result = h.after_tool_call(call, result).await;
        }
        result
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn dummy_tool_use() -> ToolUseBlock {
        ToolUseBlock {
            id: "t1".into(),
            name: "bash".into(),
            input: serde_json::Value::Null,
        }
    }

    // --- Mock hooks ---

    struct SuffixHook(String);

    #[async_trait::async_trait]
    impl Hooks for SuffixHook {
        async fn augment_system_prompt(&self, prompt: String) -> String {
            format!("{}\n{}", prompt, self.0)
        }
    }

    struct BlockAllHook;

    #[async_trait::async_trait]
    impl Hooks for BlockAllHook {
        async fn before_tool_call(&self, _: &ToolUseBlock) -> HookDecision {
            HookDecision::Block {
                reason: "blocked".into(),
            }
        }
    }

    struct ReadOnlyFilterHook;

    #[async_trait::async_trait]
    impl Hooks for ReadOnlyFilterHook {
        async fn filter_tools(&self, tools: Vec<ToolDefinition>) -> Vec<ToolDefinition> {
            tools.into_iter().filter(|t| t.read_only).collect()
        }
    }

    fn tool_def(name: &str, read_only: bool) -> ToolDefinition {
        ToolDefinition {
            name: name.into(),
            description: "test".into(),
            input_schema: serde_json::json!({}),
            read_only,
        }
    }

    // --- Tests ---

    #[tokio::test]
    async fn no_hooks_passes_through() {
        let hooks = NoHooks;
        let result = hooks.augment_system_prompt("base".into()).await;
        assert_eq!(result, "base");

        let decision = hooks.before_tool_call(&dummy_tool_use()).await;
        assert!(matches!(decision, HookDecision::Allow));
    }

    #[tokio::test]
    async fn chain_composes_system_prompt() {
        let chain = HookChain::new()
            .add(Arc::new(SuffixHook("# Hook A".into())))
            .add(Arc::new(SuffixHook("# Hook B".into())));

        let result = chain.augment_system_prompt("base".into()).await;
        assert_eq!(result, "base\n# Hook A\n# Hook B");
    }

    #[tokio::test]
    async fn chain_before_tool_call_short_circuits_on_block() {
        let chain = HookChain::new()
            .add(Arc::new(BlockAllHook))
            .add(Arc::new(SuffixHook("unreachable".into())));

        let decision = chain.before_tool_call(&dummy_tool_use()).await;
        assert!(matches!(decision, HookDecision::Block { .. }));
    }

    #[tokio::test]
    async fn chain_allow_if_no_blocks() {
        let chain = HookChain::new()
            .add(Arc::new(NoHooks))
            .add(Arc::new(NoHooks));

        let decision = chain.before_tool_call(&dummy_tool_use()).await;
        assert!(matches!(decision, HookDecision::Allow));
    }

    #[tokio::test]
    async fn chain_filter_tools_composes() {
        let chain = HookChain::new().add(Arc::new(ReadOnlyFilterHook));

        let tools = vec![
            tool_def("read", true),
            tool_def("bash", false),
            tool_def("edit", false),
        ];
        let filtered = chain.filter_tools(tools).await;
        assert_eq!(filtered.len(), 1);
        assert_eq!(filtered[0].name, "read");
    }

    #[tokio::test]
    async fn empty_chain_allows_everything() {
        let chain = HookChain::new();
        let result = chain.augment_system_prompt("base".into()).await;
        assert_eq!(result, "base");

        let tools = vec![tool_def("bash", false)];
        let filtered = chain.filter_tools(tools).await;
        assert_eq!(filtered.len(), 1);
    }
}
