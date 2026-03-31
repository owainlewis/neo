use crate::model::types::*;
use crate::model::Provider;
use futures::Stream;
use std::pin::Pin;
use tokio::sync::mpsc;
use tokio_stream::wrappers::ReceiverStream;

pub struct AnthropicProvider {
    api_key: String,
    model: String,
    max_tokens: u32,
    client: reqwest::Client,
}

impl AnthropicProvider {
    pub fn new(api_key: String, model: String, max_tokens: u32) -> Self {
        Self {
            api_key,
            model,
            max_tokens,
            client: reqwest::Client::new(),
        }
    }
}

// Request types for the Anthropic Messages API

#[derive(serde::Serialize)]
struct ApiRequest {
    model: String,
    max_tokens: u32,
    system: String,
    messages: Vec<Message>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    tools: Vec<ToolDefinition>,
    stream: bool,
}

#[async_trait::async_trait]
impl Provider for AnthropicProvider {
    async fn stream(
        &self,
        system: &str,
        messages: &[Message],
        tools: &[ToolDefinition],
    ) -> Pin<Box<dyn Stream<Item = StreamEvent> + Send>> {
        let request = ApiRequest {
            model: self.model.clone(),
            max_tokens: self.max_tokens,
            system: system.to_string(),
            messages: messages.to_vec(),
            tools: tools.to_vec(),
            stream: true,
        };

        let response = self
            .client
            .post("https://api.anthropic.com/v1/messages")
            .header("x-api-key", &self.api_key)
            .header("anthropic-version", "2023-06-01")
            .header("content-type", "application/json")
            .json(&request)
            .send()
            .await;

        let (tx, rx) = mpsc::channel(64);

        match response {
            Ok(resp) => {
                if !resp.status().is_success() {
                    let status = resp.status();
                    let body = resp.text().await.unwrap_or_default();
                    let _ = tx
                        .send(StreamEvent::Error(format!(
                            "API error {}: {}",
                            status, body
                        )))
                        .await;
                    return Box::pin(ReceiverStream::new(rx));
                }

                tokio::spawn(async move {
                    if let Err(e) = parse_sse_stream(resp, &tx).await {
                        let _ = tx.send(StreamEvent::Error(e.to_string())).await;
                    }
                });
            }
            Err(e) => {
                let _ = tx
                    .send(StreamEvent::Error(format!("Request failed: {}", e)))
                    .await;
            }
        }

        Box::pin(ReceiverStream::new(rx))
    }

    fn name(&self) -> &str {
        &self.model
    }

    fn context_window(&self) -> usize {
        200_000
    }
}

// SSE stream parser — reassembles content blocks from deltas

async fn parse_sse_stream(
    response: reqwest::Response,
    tx: &mpsc::Sender<StreamEvent>,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    use futures::StreamExt;

    let mut stream = response.bytes_stream();
    let mut buffer = String::new();

    // Track in-progress tool use blocks
    let mut current_tool_id: Option<String> = None;
    let mut current_tool_name: Option<String> = None;
    let mut current_tool_input = String::new();

    let mut usage = Usage::default();

    while let Some(chunk) = stream.next().await {
        let chunk = chunk?;
        buffer.push_str(&String::from_utf8_lossy(&chunk));

        // Process complete SSE lines
        while let Some(pos) = buffer.find("\n\n") {
            let event_text = buffer[..pos].to_string();
            buffer = buffer[pos + 2..].to_string();

            // Extract data field
            let data = event_text
                .lines()
                .filter_map(|line| line.strip_prefix("data: "))
                .collect::<Vec<_>>()
                .join("");

            if data.is_empty() || data == "[DONE]" {
                continue;
            }

            let event: serde_json::Value = match serde_json::from_str(&data) {
                Ok(v) => v,
                Err(_) => continue,
            };

            let event_type = event["type"].as_str().unwrap_or("");

            match event_type {
                "content_block_start" => {
                    let block = &event["content_block"];
                    if block["type"].as_str() == Some("tool_use") {
                        current_tool_id = block["id"].as_str().map(String::from);
                        current_tool_name = block["name"].as_str().map(String::from);
                        current_tool_input.clear();
                    }
                }
                "content_block_delta" => {
                    let delta = &event["delta"];
                    match delta["type"].as_str() {
                        Some("text_delta") => {
                            if let Some(text) = delta["text"].as_str() {
                                let _ = tx.send(StreamEvent::Text(text.to_string())).await;
                            }
                        }
                        Some("input_json_delta") => {
                            if let Some(json) = delta["partial_json"].as_str() {
                                current_tool_input.push_str(json);
                            }
                        }
                        _ => {}
                    }
                }
                "content_block_stop" => {
                    if let (Some(id), Some(name)) =
                        (current_tool_id.take(), current_tool_name.take())
                    {
                        let input: serde_json::Value =
                            serde_json::from_str(&current_tool_input).unwrap_or_default();
                        current_tool_input.clear();
                        let _ = tx
                            .send(StreamEvent::ToolUse(ToolUseBlock { id, name, input }))
                            .await;
                    }
                }
                "message_delta" => {
                    if let Some(u) = event.get("usage") {
                        if let Some(out) = u["output_tokens"].as_u64() {
                            usage.output_tokens = out as u32;
                        }
                    }
                }
                "message_start" => {
                    if let Some(u) = event["message"].get("usage") {
                        if let Some(inp) = u["input_tokens"].as_u64() {
                            usage.input_tokens = inp as u32;
                        }
                    }
                }
                "message_stop" => {
                    let _ = tx.send(StreamEvent::Done(usage.clone())).await;
                }
                "error" => {
                    let msg = event["error"]["message"]
                        .as_str()
                        .unwrap_or("Unknown error");
                    let _ = tx.send(StreamEvent::Error(msg.to_string())).await;
                }
                _ => {}
            }
        }
    }

    Ok(())
}
