use crate::provider::Provider;
use crate::types::*;
use futures::stream::BoxStream;
use futures::StreamExt;

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

// --- Wire types ---

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

// --- Provider impl ---

impl Provider for AnthropicProvider {
    fn name(&self) -> &str {
        &self.model
    }

    fn stream(&self, request: StreamRequest) -> BoxStream<'static, ProviderEvent> {
        let client = self.client.clone();
        let api_key = self.api_key.clone();
        let model = self.model.clone();
        let max_tokens = self.max_tokens;

        let (tx, rx) = tokio::sync::mpsc::channel(64);

        tokio::spawn(async move {
            let api_request = ApiRequest {
                model,
                max_tokens,
                system: request.system,
                messages: request.messages,
                tools: request.tools,
                stream: true,
            };

            let resp = match client
                .post("https://api.anthropic.com/v1/messages")
                .header("x-api-key", &api_key)
                .header("anthropic-version", "2023-06-01")
                .header("content-type", "application/json")
                .json(&api_request)
                .send()
                .await
            {
                Ok(r) => r,
                Err(e) => {
                    let _ = tx
                        .send(ProviderEvent::Error(format!("Request failed: {}", e)))
                        .await;
                    return;
                }
            };

            if !resp.status().is_success() {
                let status = resp.status();
                let body = resp.text().await.unwrap_or_default();
                let _ = tx
                    .send(ProviderEvent::Error(format!(
                        "API error {}: {}",
                        status, body
                    )))
                    .await;
                return;
            }

            consume_sse(resp, &tx).await;
        });

        // Convert the receiver into a BoxStream without a tokio-stream dep
        futures::stream::unfold(rx, |mut rx| async move {
            rx.recv().await.map(|event| (event, rx))
        })
        .boxed()
    }
}

// --- SSE parser ---

async fn consume_sse(
    resp: reqwest::Response,
    tx: &tokio::sync::mpsc::Sender<ProviderEvent>,
) {
    let mut byte_stream = resp.bytes_stream();
    let mut buffer = String::new();
    let mut block_ids: Vec<Option<String>> = Vec::new();
    let mut input_tokens: u32 = 0;
    let mut output_tokens: u32 = 0;

    while let Some(chunk) = byte_stream.next().await {
        let chunk = match chunk {
            Ok(c) => c,
            Err(e) => {
                let _ = tx
                    .send(ProviderEvent::Error(format!("Stream read error: {}", e)))
                    .await;
                return;
            }
        };

        buffer.push_str(&String::from_utf8_lossy(&chunk));

        while let Some(pos) = buffer.find('\n') {
            let line = buffer[..pos].trim_end_matches('\r').to_string();
            buffer = buffer[pos + 1..].to_string();

            let Some(data) = line.strip_prefix("data: ") else {
                continue;
            };

            if let Some(event) =
                parse_sse_event(data, &mut block_ids, &mut input_tokens, &mut output_tokens)
            {
                let is_done = matches!(event, ProviderEvent::Done { .. });
                if tx.send(event).await.is_err() {
                    return;
                }
                if is_done {
                    return;
                }
            }
        }
    }
}

fn parse_sse_event(
    data: &str,
    block_ids: &mut Vec<Option<String>>,
    input_tokens: &mut u32,
    output_tokens: &mut u32,
) -> Option<ProviderEvent> {
    let v: serde_json::Value = serde_json::from_str(data).ok()?;

    match v["type"].as_str()? {
        "message_start" => {
            if let Some(usage) = v.get("message").and_then(|m| m.get("usage")) {
                *input_tokens = usage["input_tokens"].as_u64().unwrap_or(0) as u32;
            }
            None
        }
        "content_block_start" => {
            let index = v["index"].as_u64()? as usize;
            let block = &v["content_block"];

            while block_ids.len() <= index {
                block_ids.push(None);
            }

            match block["type"].as_str()? {
                "tool_use" => {
                    let id = block["id"].as_str()?.to_string();
                    let name = block["name"].as_str()?.to_string();
                    block_ids[index] = Some(id.clone());
                    Some(ProviderEvent::ToolUseStart { id, name })
                }
                _ => {
                    block_ids[index] = None;
                    None
                }
            }
        }
        "content_block_delta" => {
            let index = v["index"].as_u64()? as usize;
            let delta = &v["delta"];

            match delta["type"].as_str()? {
                "text_delta" => Some(ProviderEvent::TextDelta(
                    delta["text"].as_str()?.to_string(),
                )),
                "input_json_delta" => {
                    let id = block_ids.get(index)?.as_ref()?.clone();
                    Some(ProviderEvent::ToolInputDelta {
                        id,
                        json_fragment: delta["partial_json"].as_str()?.to_string(),
                    })
                }
                _ => None,
            }
        }
        "content_block_stop" => {
            let index = v["index"].as_u64()? as usize;
            if let Some(Some(id)) = block_ids.get(index) {
                Some(ProviderEvent::ToolUseEnd { id: id.clone() })
            } else {
                None
            }
        }
        "message_delta" => {
            if let Some(usage) = v.get("usage") {
                *output_tokens = usage["output_tokens"].as_u64().unwrap_or(0) as u32;
            }
            let stop_reason = v
                .get("delta")
                .and_then(|d| d["stop_reason"].as_str())
                .unwrap_or("end_turn");
            Some(ProviderEvent::Done {
                usage: Usage {
                    input_tokens: *input_tokens,
                    output_tokens: *output_tokens,
                },
                stop_reason: match stop_reason {
                    "tool_use" => StopReason::ToolUse,
                    _ => StopReason::EndTurn,
                },
            })
        }
        "ping" | "message_stop" => None,
        _ => None,
    }
}
