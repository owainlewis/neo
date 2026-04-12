use neo_core::{Hooks, ToolDefinition};
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
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::Ordering;

    fn tool_def(name: &str, read_only: bool) -> ToolDefinition {
        ToolDefinition {
            name: name.into(),
            description: "test".into(),
            input_schema: serde_json::json!({}),
            read_only,
        }
    }

    #[tokio::test]
    async fn disabled_passes_prompt_through() {
        let hook = PlanModeHook::new();
        let result = hook.augment_system_prompt("base".into()).await;
        assert_eq!(result, "base");
    }

    #[tokio::test]
    async fn enabled_appends_plan_instructions() {
        let hook = PlanModeHook::new();
        hook.enabled().store(true, Ordering::Relaxed);
        let result = hook.augment_system_prompt("base".into()).await;
        assert!(result.starts_with("base"));
        assert!(result.contains("PLAN"));
        assert!(result.contains("Do NOT make any changes"));
    }

    #[tokio::test]
    async fn disabled_returns_all_tools() {
        let hook = PlanModeHook::new();
        let tools = vec![tool_def("read", true), tool_def("bash", false)];
        let filtered = hook.filter_tools(tools).await;
        assert_eq!(filtered.len(), 2);
    }

    #[tokio::test]
    async fn enabled_filters_to_read_only() {
        let hook = PlanModeHook::new();
        hook.enabled().store(true, Ordering::Relaxed);
        let tools = vec![
            tool_def("read", true),
            tool_def("bash", false),
            tool_def("edit", false),
        ];
        let filtered = hook.filter_tools(tools).await;
        assert_eq!(filtered.len(), 1);
        assert_eq!(filtered[0].name, "read");
    }

    #[tokio::test]
    async fn toggle_at_runtime() {
        let hook = PlanModeHook::new();
        let handle = hook.enabled();

        let tools = vec![tool_def("read", true), tool_def("bash", false)];

        // Off — all tools
        let filtered = hook.filter_tools(tools.clone()).await;
        assert_eq!(filtered.len(), 2);

        // On — read only
        handle.store(true, Ordering::Relaxed);
        let filtered = hook.filter_tools(tools.clone()).await;
        assert_eq!(filtered.len(), 1);

        // Off again
        handle.store(false, Ordering::Relaxed);
        let filtered = hook.filter_tools(tools).await;
        assert_eq!(filtered.len(), 2);
    }
}
