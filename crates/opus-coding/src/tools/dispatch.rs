use opus_core::{SubagentSpec, SubagentSpawner, Tool, ToolOutput, Usage};
use serde_json::json;
use std::sync::Arc;

/// Dispatch tool: fans out independent tasks to parallel subagent workers.
/// Each worker gets its own agent loop with the same tools as the parent
/// (minus dispatch itself, preventing recursive spawning).
pub struct DispatchTool {
    spawner: Arc<dyn SubagentSpawner>,
}

impl DispatchTool {
    pub fn new(spawner: Arc<dyn SubagentSpawner>) -> Self {
        Self { spawner }
    }
}

#[async_trait::async_trait]
impl Tool for DispatchTool {
    fn name(&self) -> &str {
        "dispatch"
    }

    fn description(&self) -> &str {
        "Run multiple independent tasks in parallel. Each task gets its own \
         agent with full tool access. Use this when you need to research, \
         explore, or work on several things concurrently. Tasks run simultaneously \
         and their results are collected."
    }

    fn input_schema(&self) -> serde_json::Value {
        json!({
            "type": "object",
            "properties": {
                "tasks": {
                    "type": "array",
                    "items": {
                        "type": "object",
                        "properties": {
                            "description": {
                                "type": "string",
                                "description": "What this worker should accomplish"
                            }
                        },
                        "required": ["description"]
                    },
                    "description": "List of independent tasks to run in parallel"
                },
                "max_turns_per_task": {
                    "type": "integer",
                    "description": "Maximum tool-call loops per worker (default: 20)"
                },
                "timeout_secs": {
                    "type": "integer",
                    "description": "Timeout in seconds per worker (default: 300)"
                }
            },
            "required": ["tasks"]
        })
    }

    fn is_read_only(&self) -> bool {
        false
    }

    async fn execute(&self, input: serde_json::Value) -> Result<ToolOutput, String> {
        let tasks = input["tasks"]
            .as_array()
            .ok_or("Missing 'tasks' array")?;

        if tasks.is_empty() {
            return Err("No tasks provided".into());
        }

        let max_turns = input["max_turns_per_task"].as_u64().unwrap_or(20) as usize;
        let timeout_secs = input["timeout_secs"].as_u64().unwrap_or(300);

        // Parse task descriptions
        let descriptions: Vec<String> = tasks
            .iter()
            .filter_map(|t| t["description"].as_str().map(String::from))
            .collect();

        if descriptions.is_empty() {
            return Err("No valid task descriptions found".into());
        }

        // Spawn all workers
        let mut handles = Vec::new();
        for desc in &descriptions {
            let handle = self
                .spawner
                .spawn(SubagentSpec {
                    task: desc.clone(),
                    system_prompt: None,
                    max_turns,
                    timeout_secs,
                })
                .await;
            handles.push(handle);
        }

        // Await all results (tasks are already running in parallel)
        let mut results = Vec::new();
        for handle in handles {
            results.push(handle.join().await);
        }

        // Format output and accumulate usage
        let mut total_usage = Usage::default();
        let mut output = format!("Dispatched {} workers\n", results.len());

        for (i, (desc, result)) in descriptions.iter().zip(results.iter()).enumerate() {
            total_usage.input_tokens += result.usage.input_tokens;
            total_usage.output_tokens += result.usage.output_tokens;

            output.push_str(&format!("\n## Worker {} — {}\n", i + 1, desc));
            if result.is_error {
                output.push_str(&format!("ERROR: {}\n", result.text));
            } else {
                output.push_str(&result.text);
                if !result.text.ends_with('\n') {
                    output.push('\n');
                }
            }
        }

        output.push_str(&format!(
            "\n---\nTotal subagent usage: {} input, {} output tokens",
            total_usage.input_tokens, total_usage.output_tokens
        ));

        Ok(ToolOutput::with_usage(output, total_usage))
    }
}
