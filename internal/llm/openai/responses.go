// Package openai implements the llm.Provider interface against OpenAI's
// Responses API (https://platform.openai.com/docs/api-reference/responses).
//
// Neo's internal message model is Anthropic-shaped (content blocks of type
// "text", "tool_use", "tool_result", "image"). This file holds the wire types
// and translation shared by both transports in this package:
//
//   - Client (client.go): API-key auth against api.openai.com.
//   - CodexClient (codex.go): ChatGPT/Codex subscription OAuth against
//     chatgpt.com/backend-api.
//
// Both speak the same Responses request/response shape; they differ only in
// endpoint, auth headers, and (for Codex) streaming transport. Chat Completions
// is deliberately not used — Responses is OpenAI's current API.
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

// DefaultModel is used when an API-key request carries no model.
const DefaultModel = "gpt-4o"

// --- wire types -------------------------------------------------------------

// apiRequest is the Responses API request body. Store is always false: neo is
// stateless and replays the full transcript each turn, so there is nothing to
// persist server-side. Stream is set per-transport.
type apiRequest struct {
	Model           string      `json:"model"`
	Instructions    string      `json:"instructions,omitempty"`
	Input           []inputItem `json:"input"`
	Tools           []apiTool   `json:"tools,omitempty"`
	ToolChoice      string      `json:"tool_choice,omitempty"`
	MaxOutputTokens int         `json:"max_output_tokens,omitempty"`
	Store           bool        `json:"store"`
	Stream          bool        `json:"stream,omitempty"`
}

func debugEnabled() bool {
	v := strings.TrimSpace(os.Getenv("NEO_OPENAI_DEBUG"))
	if v == "" {
		return false
	}
	enabled, err := strconv.ParseBool(v)
	return err != nil || enabled
}

func debugJSON(label string, v any) {
	if !debugEnabled() {
		return
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[openai debug] %s: <marshal error: %v>\n", label, err)
		return
	}
	fmt.Fprintf(os.Stderr, "[openai debug] %s:\n%s\n", label, b)
}

func debugHTTPResponse(prefix string, status int, raw []byte) {
	if !debugEnabled() {
		return
	}
	fmt.Fprintf(os.Stderr, "[openai debug] %s response status=%d body=%s\n", prefix, status, string(raw))
}

func buildAPIRequest(req llm.Request, model string, stream bool, toolChoice string, requireInstructions bool) apiRequest {
	instructions := systemText(req)
	if requireInstructions && instructions == "" {
		instructions = "You are a helpful assistant."
	}
	return apiRequest{
		Model:           model,
		Instructions:    instructions,
		Input:           toInput(req),
		Tools:           toAPITools(req.Tools),
		ToolChoice:      toolChoice,
		MaxOutputTokens: req.MaxTokens,
		Store:           false,
		Stream:          stream,
	}
}

// inputItem is one entry in the Responses `input` array. Unlike Chat
// Completions, tool calls and their outputs are top-level items, not fields
// nested inside a message — hence the discriminated shape.
type inputItem struct {
	Type string `json:"type"` // "message" | "function_call" | "function_call_output" | provider-specific

	// Raw is an opaque provider-specific Responses item (for example,
	// reasoning). When present, it is marshaled verbatim so stateless
	// conversations can replay items the API requires but neo does not interpret.
	Raw json.RawMessage `json:"-"`

	// type == "message"
	Role    string        `json:"role,omitempty"` // user | assistant
	Content []contentPart `json:"content,omitempty"`

	// type == "function_call"
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"` // JSON-encoded string

	// type == "function_call_output"
	Output string `json:"output,omitempty"`
}

// contentPart is a piece of a message's content. User messages carry
// input_text/input_image; assistant messages carry output_text (echoed back
// from a prior turn).
type contentPart struct {
	Type     string `json:"type"` // input_text | output_text | input_image
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"` // input_image: a data: URL
}

func (i inputItem) MarshalJSON() ([]byte, error) {
	if len(i.Raw) > 0 {
		return i.Raw, nil
	}
	type alias inputItem
	if i.Type == "function_call_output" {
		// The Responses API requires `output` on every function_call_output
		// item. The struct tag carries omitempty (so it stays absent on message
		// and function_call items), but a tool that produces no output would
		// then be dropped here and rejected with a 400
		// (missing_required_parameter: input[N].output). Emit it explicitly —
		// the shallower field shadows the embedded omitempty one.
		return json.Marshal(struct {
			alias
			Output string `json:"output"`
		}{alias(i), i.Output})
	}
	return json.Marshal(alias(i))
}

// apiTool is a Responses tool. Note the flat shape (name/description/parameters
// at the top level), unlike Chat Completions which nests them under "function".
type apiTool struct {
	Type        string         `json:"type"` // always "function"
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// apiResponse is the (non-streaming) Responses result, and is also the payload
// carried by the terminal "response.completed" streaming event — so the Codex
// client reuses this type and toResponse to assemble its blocking result.
type apiResponse struct {
	Status            string         `json:"status"`
	Output            []outputItem   `json:"output"`
	IncompleteDetails *incomplete    `json:"incomplete_details,omitempty"`
	Usage             *responseUsage `json:"usage,omitempty"`
	Error             *responseError `json:"error,omitempty"`
}

func (r *apiResponse) UnmarshalJSON(data []byte) error {
	type wire struct {
		Status            string            `json:"status"`
		Output            []json.RawMessage `json:"output"`
		IncompleteDetails *incomplete       `json:"incomplete_details,omitempty"`
		Usage             *responseUsage    `json:"usage,omitempty"`
		Error             *responseError    `json:"error,omitempty"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	r.Status = w.Status
	r.IncompleteDetails = w.IncompleteDetails
	r.Usage = w.Usage
	r.Error = w.Error
	r.Output = make([]outputItem, 0, len(w.Output))
	for _, raw := range w.Output {
		var item outputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		item.Raw = append(item.Raw[:0], raw...)
		r.Output = append(r.Output, item)
	}
	return nil
}

type incomplete struct {
	Reason string `json:"reason"`
}

type responseError struct {
	Message string `json:"message"`
}

type responseUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	InputTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details,omitempty"`
}

// outputItem is one entry in the response `output` array.
type outputItem struct {
	Type    string          `json:"type"` // message | function_call | reasoning | ...
	Raw     json.RawMessage `json:"-"`
	Role    string          `json:"role"`
	Content []struct {
		Type string `json:"type"` // output_text
		Text string `json:"text"`
	} `json:"content"`

	// type == "function_call"
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// --- translation: llm.Request -> Responses ----------------------------------

// systemText flattens the request's system prompt into the `instructions`
// field. The Responses API has a single instructions string rather than
// cache-breakpoint blocks, so SystemBlocks are concatenated.
func systemText(req llm.Request) string {
	if len(req.SystemBlocks) == 0 {
		return req.System
	}
	var b strings.Builder
	for _, blk := range req.SystemBlocks {
		if blk.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(blk.Text)
	}
	return b.String()
}

// toInput converts neo's content-block messages into Responses input items.
// Assistant tool_use blocks and user tool_result blocks become top-level
// function_call / function_call_output items; text and images become message
// items with the role-appropriate content parts.
func toInput(req llm.Request) []inputItem {
	out := make([]inputItem, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case llm.RoleAssistant:
			var parts []contentPart
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					if b.Text != "" {
						parts = append(parts, contentPart{Type: "output_text", Text: b.Text})
					}
				case "tool_use":
					args, _ := json.Marshal(b.Input)
					out = append(out, inputItem{
						Type:      "function_call",
						CallID:    b.ID,
						Name:      b.Name,
						Arguments: string(args),
					})
				case "raw":
					if len(b.Raw) > 0 {
						out = append(out, inputItem{Type: "raw", Raw: b.Raw})
					}
				}
			}
			if len(parts) > 0 {
				out = append(out, inputItem{Type: "message", Role: "assistant", Content: parts})
			}

		case llm.RoleUser, llm.RoleTool:
			// A user message may carry tool_result blocks (the agent records tool
			// outputs on a user-role message) and/or plain text and images.
			var parts []contentPart
			for _, b := range m.Content {
				switch b.Type {
				case "tool_result":
					out = append(out, inputItem{
						Type:   "function_call_output",
						CallID: b.ToolUseID,
						Output: b.Content,
					})
				case "text":
					if b.Text != "" {
						parts = append(parts, contentPart{Type: "input_text", Text: b.Text})
					}
				case "image":
					if b.Source != nil && b.Source.Data != "" {
						parts = append(parts, contentPart{
							Type:     "input_image",
							ImageURL: "data:" + b.Source.MediaType + ";base64," + b.Source.Data,
						})
					}
				}
			}
			if len(parts) > 0 {
				out = append(out, inputItem{Type: "message", Role: "user", Content: parts})
			}
		}
	}
	return out
}

func toAPITools(tools []llm.ToolSpec) []apiTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]apiTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, apiTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		})
	}
	return out
}

// --- translation: Responses -> llm.Response ---------------------------------

func toResponse(out apiResponse) (*llm.Response, error) {
	if out.Error != nil {
		return nil, fmt.Errorf("openai: %s", out.Error.Message)
	}

	var content []llm.ContentBlock
	sawToolCall := false
	for _, item := range out.Output {
		switch item.Type {
		case "message":
			var text strings.Builder
			for _, p := range item.Content {
				if p.Type == "output_text" {
					text.WriteString(p.Text)
				}
			}
			if text.Len() > 0 {
				content = append(content, llm.ContentBlock{Type: "text", Text: text.String()})
			}
		case "function_call":
			sawToolCall = true
			input := map[string]any{}
			if item.Arguments != "" {
				// Arguments is a JSON string; on a decode error pass an empty object
				// rather than failing the whole turn.
				_ = json.Unmarshal([]byte(item.Arguments), &input)
			}
			content = append(content, llm.ContentBlock{
				Type:  "tool_use",
				ID:    item.CallID,
				Name:  item.Name,
				Input: input,
			})
		case "reasoning":
			if len(item.Raw) > 0 {
				content = append(content, llm.ContentBlock{Type: "raw", Raw: item.Raw})
			}
		}
		// Other item types are ignored: neo only understands text and tools, plus
		// opaque reasoning items that must be replayed for stateless Responses API
		// conversations on reasoning models.
	}

	return &llm.Response{
		Content:    content,
		StopReason: stopReason(out, sawToolCall),
		Usage:      toUsage(out.Usage),
	}, nil
}

// stopReason normalizes the Responses status into neo's stop-reason vocabulary
// (which mirrors Anthropic's). The agent loop keys off "tool_use".
func stopReason(out apiResponse, sawToolCall bool) string {
	if sawToolCall {
		return "tool_use"
	}
	if out.Status == "incomplete" && out.IncompleteDetails != nil &&
		out.IncompleteDetails.Reason == "max_output_tokens" {
		return "max_tokens"
	}
	return "end_turn"
}

func toUsage(u *responseUsage) llm.Usage {
	if u == nil {
		return llm.Usage{}
	}
	usage := llm.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}
	if u.InputTokensDetails != nil {
		usage.CacheReadTokens = u.InputTokensDetails.CachedTokens
	}
	return usage
}

// --- shared transport / retry ----------------------------------------------

// backoffDelay grows exponentially from base, capped at 30s.
func backoffDelay(base time.Duration, attempt int) time.Duration {
	d := base << attempt // 500ms, 1s, 2s, 4s, ...
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

func delayWithHeader(base time.Duration, attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > 30*time.Second {
			return 30 * time.Second
		}
		return retryAfter
	}
	return backoffDelay(base, attempt)
}

// parseRetryAfterHeader reads a Retry-After header value (seconds form).
func parseRetryAfterHeader(v string) time.Duration {
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		return time.Duration(n) * time.Second
	}
	return 0
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
