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
