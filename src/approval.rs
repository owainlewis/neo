use crate::ui::{self, ApprovalRequest};
use neo_core::{HookDecision, Hooks, ToolUseBlock};
use std::sync::mpsc::Sender;

/// Approval hook: intercepts write tools and asks the user for confirmation
/// via the TUI approval channel. Read-only tools and any tool not in
/// `write_tool_names` pass through untouched.
pub struct ApprovalHook {
    pub approval_tx: Sender<ApprovalRequest>,
    pub write_tool_names: Vec<String>,
}

#[async_trait::async_trait]
impl Hooks for ApprovalHook {
    async fn before_tool_call(&self, call: &ToolUseBlock) -> HookDecision {
        if !self.write_tool_names.iter().any(|n| n == &call.name) {
            return HookDecision::Allow;
        }

        let (resp_tx, resp_rx) = tokio::sync::oneshot::channel();
        let summary = ui::tool_input_summary(
            &call.name,
            &serde_json::to_string(&call.input).unwrap_or_default(),
        );
        let _ = self.approval_tx.send(ApprovalRequest {
            tool_name: call.name.clone(),
            summary,
            responder: resp_tx,
        });

        // Await the UI response without blocking the Tokio runtime.
        let approved = resp_rx.await.unwrap_or(false);

        if approved {
            HookDecision::Allow
        } else {
            HookDecision::Block {
                reason: "Tool use denied by user.".into(),
            }
        }
    }
}
