// Package openai implements the llm.Provider interface against the OpenAI
// Chat Completions API using API-key authentication.
//
// Neo's internal message model is Anthropic-shaped (content blocks of type
// "text", "tool_use", "tool_result", "image"). This package translates that
// model to and from the OpenAI Chat Completions wire format so the core agent
// loop stays provider-agnostic. Subscription/OIDC auth is intentionally out of
// scope here (tracked separately); only OPENAI_API_KEY is supported.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

const defaultEndpoint = "https://api.openai.com/v1/chat/completions"

// DefaultModel is used when a request carries no model.
const DefaultModel = "gpt-4o"

type Client struct {
	APIKey     string
	Endpoint   string
	HTTP       *http.Client
	MaxRetries int           // default: 4
	BaseDelay  time.Duration // default: 500ms
}

// New constructs a Client from the OPENAI_API_KEY environment variable.
func New() (*Client, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is not set")
	}
	return &Client{
		APIKey:     key,
		Endpoint:   defaultEndpoint,
		HTTP:       &http.Client{Timeout: 5 * time.Minute},
		MaxRetries: 4,
		BaseDelay:  500 * time.Millisecond,
	}, nil
}

func (c *Client) Name() string { return "openai" }

// --- wire types -------------------------------------------------------------

type apiRequest struct {
	Model     string       `json:"model"`
	Messages  []apiMessage `json:"messages"`
	Tools     []apiTool    `json:"tools,omitempty"`
	MaxTokens int          `json:"max_tokens,omitempty"`
}

type apiMessage struct {
	Role       string        `json:"role"` // system | user | assistant | tool
	Content    string        `json:"content,omitempty"`
	ToolCalls  []apiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type apiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"` // always "function"
	Function apiToolCallFunc `json:"function"`
}

type apiToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON-encoded string
}

type apiTool struct {
	Type     string      `json:"type"` // always "function"
	Function apiToolSpec `json:"function"`
}

type apiToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type apiResponse struct {
	Choices []struct {
		Message struct {
			Content   string        `json:"content"`
			ToolCalls []apiToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// --- translation: llm.Request -> OpenAI -------------------------------------

// systemText flattens the request's system prompt. OpenAI has no prompt-cache
// breakpoints, so SystemBlocks are simply concatenated; otherwise the plain
// System string is used.
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

// toAPIMessages converts neo's content-block messages into OpenAI chat
// messages. tool_use blocks become assistant tool_calls; tool_result blocks
// become separate "tool" role messages keyed by tool_call_id.
func toAPIMessages(req llm.Request) []apiMessage {
	out := make([]apiMessage, 0, len(req.Messages)+1)
	if sys := systemText(req); sys != "" {
		out = append(out, apiMessage{Role: "system", Content: sys})
	}

	for _, m := range req.Messages {
		switch m.Role {
		case llm.RoleAssistant:
			msg := apiMessage{Role: "assistant"}
			var text strings.Builder
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					text.WriteString(b.Text)
				case "tool_use":
					args, _ := json.Marshal(b.Input)
					msg.ToolCalls = append(msg.ToolCalls, apiToolCall{
						ID:   b.ID,
						Type: "function",
						Function: apiToolCallFunc{
							Name:      b.Name,
							Arguments: string(args),
						},
					})
				}
			}
			msg.Content = text.String()
			out = append(out, msg)

		case llm.RoleUser, llm.RoleTool:
			// A user message may carry tool_result blocks (the agent records
			// tool outputs on a user-role message) and/or plain text. Each
			// tool_result becomes its own "tool" message; remaining text becomes
			// a "user" message.
			var text strings.Builder
			for _, b := range m.Content {
				switch b.Type {
				case "tool_result":
					out = append(out, apiMessage{
						Role:       "tool",
						ToolCallID: b.ToolUseID,
						Content:    b.Content,
					})
				case "text":
					text.WriteString(b.Text)
				case "image":
					// OpenAI image input uses a structured content array; not yet
					// supported here. Note it so the model isn't left confused.
					text.WriteString("[image omitted: not supported by the openai provider]")
				}
			}
			if text.Len() > 0 {
				out = append(out, apiMessage{Role: "user", Content: text.String()})
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
			Type: "function",
			Function: apiToolSpec{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return out
}

// --- translation: OpenAI -> llm.Response ------------------------------------

func toResponse(out apiResponse) (*llm.Response, error) {
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("openai: no choices in response")
	}
	choice := out.Choices[0]

	var content []llm.ContentBlock
	if choice.Message.Content != "" {
		content = append(content, llm.ContentBlock{Type: "text", Text: choice.Message.Content})
	}
	for _, tc := range choice.Message.ToolCalls {
		input := map[string]any{}
		if tc.Function.Arguments != "" {
			// Arguments is a JSON string; ignore decode errors and pass an empty
			// object rather than failing the whole turn.
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		}
		content = append(content, llm.ContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	resp := &llm.Response{
		Content:    content,
		StopReason: mapFinishReason(choice.FinishReason),
	}
	if out.Usage != nil {
		resp.Usage = llm.Usage{
			InputTokens:  out.Usage.PromptTokens,
			OutputTokens: out.Usage.CompletionTokens,
		}
	}
	return resp, nil
}

// mapFinishReason normalizes OpenAI finish reasons to neo's stop-reason
// vocabulary (which mirrors Anthropic's). The agent loop keys off "tool_use".
func mapFinishReason(r string) string {
	switch r {
	case "tool_calls":
		return "tool_use"
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	default:
		return r
	}
}

// --- request driving --------------------------------------------------------

func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	body, err := json.Marshal(apiRequest{
		Model:     model,
		Messages:  toAPIMessages(req),
		Tools:     toAPITools(req.Tools),
		MaxTokens: req.MaxTokens,
	})
	if err != nil {
		return nil, err
	}

	maxRetries := c.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	baseDelay := c.BaseDelay
	if baseDelay <= 0 {
		baseDelay = 500 * time.Millisecond
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		raw, status, retryAfter, err := c.doRequest(ctx, body)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt == maxRetries {
				return nil, err
			}
			if err := sleep(ctx, backoffDelay(baseDelay, attempt)); err != nil {
				return nil, err
			}
			continue
		}

		if status == 429 || status >= 500 {
			lastErr = fmt.Errorf("openai %d: %s", status, string(raw))
			if attempt == maxRetries {
				return nil, lastErr
			}
			if err := sleep(ctx, delayWithHeader(baseDelay, attempt, retryAfter)); err != nil {
				return nil, err
			}
			continue
		}
		if status >= 400 {
			return nil, fmt.Errorf("openai %d: %s", status, string(raw))
		}

		var out apiResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
		}
		if out.Error != nil {
			return nil, fmt.Errorf("openai: %s", out.Error.Message)
		}
		return toResponse(out)
	}
	return nil, lastErr
}

// doRequest issues one POST and returns the body, status, and any Retry-After
// hint from the response header.
func (c *Client) doRequest(ctx context.Context, body []byte) ([]byte, int, time.Duration, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, 0, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, parseRetryAfterHeader(resp.Header.Get("Retry-After")), nil
}

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
