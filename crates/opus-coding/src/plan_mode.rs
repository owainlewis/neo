use opus_core::{HookDecision, Hooks, Message, ToolDefinition, ToolResult, ToolUseBlock};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

/// Plan mode: when enabled, hides non-read-only tools from the model and
/// appends planning instructions to the system prompt.
///
/// Toggle via the `Arc<AtomicBool>` returned by `enabled()` — this lets the
/// binary flip the flag in response to slash commands without owning the hook
/// directly.
pub struct PlanModeHook {
    enabled: Arc<AtomicBool>,
}

impl PlanModeHook {
    pub fn new() -> Self {
        Self {
            enabled: Arc::new(AtomicBool::new(false)),
        }
    }

    /// Handle for toggling plan mode from outside the hook chain.
    pub fn enabled(&self) -> Arc<AtomicBool> {
        self.enabled.clone()
    }

    fn is_on(&self) -> bool {
        self.enabled.load(Ordering::Relaxed)
    }
}

impl Default for PlanModeHook {
    fn default() -> Self {
        Self::new()
    }
}

#[async_trait::async_trait]
impl Hooks for PlanModeHook {
    async fn augment_system_prompt(&self, prompt: String) -> String {
        if !self.is_on() {
            return prompt;
        }
        format!(
            "{}\n\n# Mode: PLAN\n\
             You are in plan mode. Research the codebase using your read-only tools, \
             then produce a concrete step-by-step plan. Do NOT make any changes. \
             List exactly which files you would edit and what you would change.",
            prompt
        )
    }

    async fn filter_tools(&self, tools: Vec<ToolDefinition>) -> Vec<ToolDefinition> {
        if !self.is_on() {
            return tools;
        }
        tools.into_iter().filter(|t| t.read_only).collect()
    }

    async fn before_tool_call(&self, _call: &ToolUseBlock) -> HookDecision {
        HookDecision::Allow
    }

    async fn after_tool_call(&self, _call: &ToolUseBlock, result: ToolResult) -> ToolResult {
        result
    }

    async fn transform_context(&self, _messages: &mut Vec<Message>) {}
}
