package anthropic

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/owainlewis/neo/internal/llm"
)

// maxStreamLineBytes bounds one SSE data line. Deltas are small; a line beyond
// this means the stream is not what we think it is.
const maxStreamLineBytes = 1 << 20

// streamEvent is the union of the Messages API streaming events, decoded far
// enough to reassemble a complete response. Events we do not need (ping,
// content_block_stop for text) decode into an ignored type.
type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage *apiUsage `json:"usage"`
	} `json:"message"`
	ContentBlock *llm.ContentBlock `json:"content_block"`
	Delta        *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *apiUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// parseStream reassembles a streaming response into the same value the
// non-streaming endpoint would have returned. Nothing is emitted as it
// arrives: Neo renders completed blocks, and streaming exists here so a long
// generation is not bounded by a single request timeout, not to paint text.
func parseStream(r io.Reader) (*llm.Response, error) {
	var (
		out       llm.Response
		toolJSON  = map[int]*strings.Builder{}
		blockAt   = map[int]int{} // event index -> position in out.Content
		sawResult bool
	)

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxStreamLineBytes)
	for scanner.Scan() {
		payload, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue // event: lines and blank separators carry nothing we need
		}
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return nil, fmt.Errorf("decode stream event: %w (data: %s)", err, payload)
		}
		if ev.Error != nil {
			return nil, fmt.Errorf("anthropic: %s", ev.Error.Message)
		}

		switch ev.Type {
		case "message_start":
			sawResult = true
			if ev.Message != nil && ev.Message.Usage != nil {
				out.Usage = ev.Message.Usage.toLLM()
			}
		case "content_block_start":
			if ev.ContentBlock == nil {
				continue
			}
			blockAt[ev.Index] = len(out.Content)
			out.Content = append(out.Content, *ev.ContentBlock)
			if ev.ContentBlock.Type == "tool_use" {
				toolJSON[ev.Index] = &strings.Builder{}
			}
		case "content_block_delta":
			pos, ok := blockAt[ev.Index]
			if !ok || ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				out.Content[pos].Text += ev.Delta.Text
			case "input_json_delta":
				if b := toolJSON[ev.Index]; b != nil {
					b.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "content_block_stop":
			pos, ok := blockAt[ev.Index]
			if !ok {
				continue
			}
			b := toolJSON[ev.Index]
			if b == nil {
				continue
			}
			// Tool inputs arrive as concatenated JSON fragments. An empty
			// buffer means a tool with no arguments, which is valid.
			if raw := b.String(); raw != "" {
				if err := json.Unmarshal([]byte(raw), &out.Content[pos].Input); err != nil {
					return nil, fmt.Errorf("decode tool input for %s: %w", out.Content[pos].Name, err)
				}
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				out.StopReason = ev.Delta.StopReason
			}
			// The final output count only appears here; the input counts from
			// message_start are already correct and must not be overwritten.
			if ev.Usage != nil && ev.Usage.OutputTokens > 0 {
				out.Usage.OutputTokens = ev.Usage.OutputTokens
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}
	if !sawResult {
		return nil, fmt.Errorf("anthropic: stream ended before any response")
	}
	return &out, nil
}
