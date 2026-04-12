use serde::{Deserialize, Serialize};

// --- Messages sent to the API ---

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "role")]
pub enum Message {
    #[serde(rename = "user")]
    User { content: Vec<ContentBlock> },
    #[serde(rename = "assistant")]
    Assistant { content: Vec<ContentBlock> },
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type")]
pub enum ContentBlock {
    #[serde(rename = "text")]
    Text { text: String },
    #[serde(rename = "tool_use")]
    ToolUse {
        id: String,
        name: String,
        input: serde_json::Value,
    },
    #[serde(rename = "tool_result")]
    ToolResult {
        tool_use_id: String,
        content: String,
        #[serde(skip_serializing_if = "Option::is_none")]
        is_error: Option<bool>,
    },
}

// --- Tool definitions sent to the API ---

#[derive(Debug, Clone, Serialize)]
pub struct ToolDefinition {
    pub name: String,
    pub description: String,
    pub input_schema: serde_json::Value,
    #[serde(skip)]
    pub read_only: bool,
}

// --- Tool use / result ---

#[derive(Debug, Clone)]
pub struct ToolUseBlock {
    pub id: String,
    pub name: String,
    pub input: serde_json::Value,
}

/// Return value from `Tool::execute`. Most tools return `ToolOutput::text(...)`;
/// tools that consume tokens (e.g. dispatch/subagents) attach usage.
#[derive(Debug, Clone)]
pub struct ToolOutput {
    pub content: String,
    pub usage: Option<Usage>,
}

impl ToolOutput {
    pub fn text(content: String) -> Self {
        Self {
            content,
            usage: None,
        }
    }
    pub fn with_usage(content: String, usage: Usage) -> Self {
        Self {
            content,
            usage: Some(usage),
        }
    }
}

#[derive(Debug, Clone)]
pub struct ToolResult {
    pub tool_use_id: String,
    pub content: String,
    pub is_error: bool,
    /// Token usage from tools that run subagents. Accumulated into
    /// `AgentState.total_usage` by the agent loop.
    pub usage: Option<Usage>,
}

// --- Token usage ---

#[derive(Debug, Clone, Default)]
pub struct Usage {
    pub input_tokens: u32,
    pub output_tokens: u32,
}

// --- Streaming provider types ---

/// Owned request for a streaming provider call.
pub struct StreamRequest {
    pub system: String,
    pub messages: Vec<Message>,
    pub tools: Vec<ToolDefinition>,
}

/// Events emitted by a streaming provider. The stream contract: items never
/// panic; errors are a terminal `Error` event. After `Done` or `Error` the
/// stream yields no further items.
#[derive(Debug, Clone)]
pub enum ProviderEvent {
    TextDelta(String),
    ToolUseStart { id: String, name: String },
    ToolInputDelta { id: String, json_fragment: String },
    ToolUseEnd { id: String },
    Done { usage: Usage, stop_reason: StopReason },
    Error(String),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum StopReason {
    EndTurn,
    ToolUse,
}
