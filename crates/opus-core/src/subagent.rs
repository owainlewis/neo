use crate::agent::{run_turn, AgentEvent, AgentState};
use crate::hooks::NoHooks;
use crate::provider::Provider;
use crate::tool::Registry;
use crate::types::Usage;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

// --- Trait + types ---

/// Spec for launching a subagent worker.
pub struct SubagentSpec {
    /// The task description — becomes the user message.
    pub task: String,
    /// Custom system prompt. `None` = inherit the parent's.
    pub system_prompt: Option<String>,
    /// Maximum tool-call loops before the subagent gives up.
    pub max_turns: usize,
}

/// Opaque handle to a running subagent. Call `join()` to await its result.
pub struct SubagentHandle {
    pub id: String,
    result: tokio::task::JoinHandle<SubagentResult>,
}

impl SubagentHandle {
    pub async fn join(self) -> SubagentResult {
        self.result.await.unwrap_or_else(|e| SubagentResult {
            text: format!("Subagent panicked: {}", e),
            usage: Usage::default(),
            is_error: true,
        })
    }
}

/// Final output from a completed subagent.
pub struct SubagentResult {
    pub text: String,
    pub usage: Usage,
    pub is_error: bool,
}

/// Factory for spawning subagent workers. The core ships a `DefaultSpawner`;
/// consumers can implement their own for sandboxed or remote execution.
#[async_trait::async_trait]
pub trait SubagentSpawner: Send + Sync {
    async fn spawn(&self, spec: SubagentSpec) -> SubagentHandle;
}

// --- Default implementation ---

/// Spawns subagents as local Tokio tasks reusing the parent's provider and
/// tool registry. Subagents run with `NoHooks` (no approval prompts, no plan
/// mode) — their trust boundary is the parent's approval of the `dispatch`
/// tool call.
pub struct DefaultSpawner {
    provider: Arc<dyn Provider>,
    registry: Arc<Registry>,
    system_prompt: String,
    next_id: AtomicU64,
}

impl DefaultSpawner {
    pub fn new(
        provider: Arc<dyn Provider>,
        registry: Arc<Registry>,
        system_prompt: String,
    ) -> Self {
        Self {
            provider,
            registry,
            system_prompt,
            next_id: AtomicU64::new(0),
        }
    }
}

#[async_trait::async_trait]
impl SubagentSpawner for DefaultSpawner {
    async fn spawn(&self, spec: SubagentSpec) -> SubagentHandle {
        let id = format!("sub-{}", self.next_id.fetch_add(1, Ordering::Relaxed));
        let provider = self.provider.clone();
        let registry = self.registry.clone();
        let system_prompt = spec
            .system_prompt
            .unwrap_or_else(|| self.system_prompt.clone());
        let task = spec.task;
        let max_turns = spec.max_turns;

        let handle = tokio::spawn(async move {
            let mut state = AgentState::new(max_turns, system_prompt);
            state.add_user_message(&task);

            let mut output = String::new();
            let mut error: Option<String> = None;
            let hooks = NoHooks;

            run_turn(
                &mut state,
                &*provider,
                &*registry,
                &hooks,
                &mut |ev| match ev {
                    AgentEvent::TextDelta(t) => output.push_str(t),
                    AgentEvent::Text(t) => output.push_str(t),
                    AgentEvent::Error(e) => error = Some(e.clone()),
                    _ => {}
                },
            )
            .await;

            match error {
                Some(e) => SubagentResult {
                    text: e,
                    usage: state.total_usage,
                    is_error: true,
                },
                None => SubagentResult {
                    text: output,
                    usage: state.total_usage,
                    is_error: false,
                },
            }
        });

        SubagentHandle {
            id,
            result: handle,
        }
    }
}
