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

    // Process any remaining partial line left in the buffer after the stream
    // closes. If the final SSE data line arrived without a trailing newline,
    // dropping it would lose the Done event and hang the consumer.
    let remaining = buffer.trim();
    if let Some(data) = remaining.strip_prefix("data: ") {
        if let Some(event) =
            parse_sse_event(data, &mut block_ids, &mut input_tokens, &mut output_tokens)
        {
            let _ = tx.send(event).await;
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

#[cfg(test)]
mod tests {
    use super::*;

    fn parse(data: &str, block_ids: &mut Vec<Option<String>>) -> Option<ProviderEvent> {
        let mut input = 0u32;
        let mut output = 0u32;
        parse_sse_event(data, block_ids, &mut input, &mut output)
    }

    #[test]
    fn text_delta() {
        let mut ids = vec![];
        let event = parse(
            r#"{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}"#,
            &mut ids,
        );
        assert!(matches!(event, Some(ProviderEvent::TextDelta(t)) if t == "Hello"));
    }

    #[test]
    fn tool_use_start() {
        let mut ids = vec![];
        let event = parse(
            r#"{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"bash"}}"#,
            &mut ids,
        );
        assert!(matches!(event, Some(ProviderEvent::ToolUseStart { ref id, ref name }) if id == "t1" && name == "bash"));
        assert_eq!(ids[0], Some("t1".to_string()));
    }

    #[test]
    fn tool_input_delta_maps_to_correct_id() {
        let mut ids = vec![Some("t1".into())];
        let event = parse(
            r#"{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\":"}}"#,
            &mut ids,
        );
        assert!(matches!(event, Some(ProviderEvent::ToolInputDelta { ref id, .. }) if id == "t1"));
    }

    #[test]
    fn tool_use_end() {
        let mut ids = vec![Some("t1".into())];
        let event = parse(
            r#"{"type":"content_block_stop","index":0}"#,
            &mut ids,
        );
        assert!(matches!(event, Some(ProviderEvent::ToolUseEnd { ref id }) if id == "t1"));
    }

    #[test]
    fn content_block_stop_for_text_returns_none() {
        let mut ids = vec![None]; // index 0 was a text block
        let event = parse(r#"{"type":"content_block_stop","index":0}"#, &mut ids);
        assert!(event.is_none());
    }

    #[test]
    fn message_start_captures_input_tokens() {
        let mut ids = vec![];
        let mut input = 0u32;
        let mut output = 0u32;
        let event = parse_sse_event(
            r#"{"type":"message_start","message":{"usage":{"input_tokens":150}}}"#,
            &mut ids,
            &mut input,
            &mut output,
        );
        assert!(event.is_none());
        assert_eq!(input, 150);
    }

    #[test]
    fn message_delta_emits_done_with_usage() {
        let mut ids = vec![];
        let mut input = 100u32;
        let mut output = 0u32;
        let event = parse_sse_event(
            r#"{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42}}"#,
            &mut ids,
            &mut input,
            &mut output,
        );
        match event {
            Some(ProviderEvent::Done { usage, stop_reason }) => {
                assert_eq!(usage.input_tokens, 100);
                assert_eq!(usage.output_tokens, 42);
                assert_eq!(stop_reason, StopReason::EndTurn);
            }
            _ => panic!("expected Done"),
        }
    }

    #[test]
    fn message_delta_tool_use_stop_reason() {
        let mut ids = vec![];
        let mut input = 0u32;
        let mut output = 0u32;
        let event = parse_sse_event(
            r#"{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":10}}"#,
            &mut ids,
            &mut input,
            &mut output,
        );
        assert!(matches!(event, Some(ProviderEvent::Done { stop_reason: StopReason::ToolUse, .. })));
    }

    #[test]
    fn ping_returns_none() {
        let mut ids = vec![];
        assert!(parse(r#"{"type":"ping"}"#, &mut ids).is_none());
    }

    #[test]
    fn invalid_json_returns_none() {
        let mut ids = vec![];
        assert!(parse("not json at all", &mut ids).is_none());
    }

    #[test]
    fn multiple_content_blocks_tracked_by_index() {
        let mut ids = vec![];
        // Text block at index 0
        parse(
            r#"{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}"#,
            &mut ids,
        );
        // Tool at index 1
        parse(
            r#"{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"read"}}"#,
            &mut ids,
        );
        assert_eq!(ids.len(), 2);
        assert_eq!(ids[0], None);
        assert_eq!(ids[1], Some("t1".into()));

        // Delta on index 1 maps to t1
        let event = parse(
            r#"{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}"#,
            &mut ids,
        );
        assert!(matches!(event, Some(ProviderEvent::ToolInputDelta { ref id, .. }) if id == "t1"));
    }
}
