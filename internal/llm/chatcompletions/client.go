// Package chatcompletions implements llm.Provider for OpenAI-compatible
// Chat Completions APIs.
package chatcompletions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/retry"
)

// Client talks to an OpenAI-compatible Chat Completions endpoint.
type Client struct {
	ProviderName string
	APIKey       string
	Endpoint     string
	DefaultModel string
	HTTP         *http.Client
	MaxRetries   int
	BaseDelay    time.Duration
}

func (c *Client) Name() string { return c.ProviderName }

func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = c.DefaultModel
	}
	apiReq := BuildRequest(req, model)
	body, err := json.Marshal(apiReq)
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
			if err := sleep(ctx, retry.Delay(baseDelay, attempt, retry.Absent())); err != nil {
				return nil, err
			}
			continue
		}
		if status == 429 || status >= 500 {
			lastErr = fmt.Errorf("%s %d: %s", c.ProviderName, status, string(raw))
			if attempt == maxRetries {
				return nil, lastErr
			}
			if err := sleep(ctx, retry.Delay(baseDelay, attempt, retryAfter)); err != nil {
				return nil, err
			}
			continue
		}
		if status >= 400 {
			return nil, fmt.Errorf("%s %d: %s", c.ProviderName, status, string(raw))
		}
		var out Response
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
		}
		return ToLLMResponse(out)
	}
	return nil, lastErr
}

func (c *Client) doRequest(ctx context.Context, body []byte) ([]byte, int, retry.RetryAfter, error) {
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, retry.Absent(), err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, 0, retry.Absent(), err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, retry.Absent(), fmt.Errorf("read response body: %w", err)
	}
	return raw, resp.StatusCode, retry.ParseRetryAfterHeader(resp.Header.Get("Retry-After"), time.Now()), nil
}

// Request is the Chat Completions request body.
type Request struct {
	Model      string    `json:"model"`
	Messages   []Message `json:"messages"`
	Tools      []Tool    `json:"tools,omitempty"`
	ToolChoice string    `json:"tool_choice,omitempty"`
	MaxTokens  int       `json:"max_tokens,omitempty"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

type ToolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

type FunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// Response is the Chat Completions response body.
type Response struct {
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type Choice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// BuildRequest converts Neo's provider-neutral request to Chat Completions.
func BuildRequest(req llm.Request, model string) Request {
	messages := make([]Message, 0, len(req.Messages)+1)
	if system := systemText(req); system != "" {
		messages = append(messages, Message{Role: "system", Content: system})
	}
	for _, m := range req.Messages {
		messages = append(messages, toMessages(m)...)
	}
	out := Request{Model: model, Messages: messages, Tools: toTools(req.Tools), MaxTokens: req.MaxTokens}
	if len(out.Tools) > 0 {
		out.ToolChoice = "auto"
	}
	return out
}

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

func messageContentText(v any) string {
	s, _ := v.(string)
	return s
}

func toMessages(m llm.Message) []Message {
	switch m.Role {
	case llm.RoleAssistant:
		var content strings.Builder
		var calls []ToolCall
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				content.WriteString(b.Text)
			case "tool_use":
				args, _ := json.Marshal(b.Input)
				calls = append(calls, ToolCall{ID: b.ID, Type: "function", Function: FunctionCall{Name: b.Name, Arguments: string(args)}})
			}
		}
		if content.Len() == 0 && len(calls) == 0 {
			return nil
		}
		return []Message{{Role: "assistant", Content: content.String(), ToolCalls: calls}}
	case llm.RoleUser, llm.RoleTool:
		var out []Message
		var text strings.Builder
		var parts []ContentPart
		flushUser := func() {
			if text.Len() == 0 && len(parts) == 0 {
				return
			}
			if len(parts) == 0 {
				out = append(out, Message{Role: "user", Content: text.String()})
				text.Reset()
				return
			}
			if text.Len() > 0 {
				parts = append(parts, ContentPart{Type: "text", Text: text.String()})
			}
			out = append(out, Message{Role: "user", Content: parts})
			text.Reset()
			parts = nil
		}
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				text.WriteString(b.Text)
			case "image":
				if b.Source != nil && b.Source.Data != "" {
					mediaType := b.Source.MediaType
					if mediaType == "" {
						mediaType = "application/octet-stream"
					}
					parts = append(parts, ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: "data:" + mediaType + ";base64," + b.Source.Data}})
				}
			case "tool_result":
				flushUser()
				out = append(out, Message{Role: "tool", ToolCallID: b.ToolUseID, Content: b.Content})
			}
		}
		flushUser()
		return out
	default:
		return nil
	}
}

func toTools(tools []llm.ToolSpec) []Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, Tool{Type: "function", Function: FunctionSpec{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}})
	}
	return out
}

// ToLLMResponse converts Chat Completions output into Neo's neutral response.
func ToLLMResponse(out Response) (*llm.Response, error) {
	if out.Error != nil {
		return nil, fmt.Errorf("chat completions: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("chat completions: no choices returned")
	}
	choice := out.Choices[0]
	var content []llm.ContentBlock
	if text := messageContentText(choice.Message.Content); text != "" {
		content = append(content, llm.ContentBlock{Type: "text", Text: text})
	}
	for _, tc := range choice.Message.ToolCalls {
		input := map[string]any{}
		if tc.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		}
		content = append(content, llm.ContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: input})
	}
	return &llm.Response{Content: content, StopReason: stopReason(choice), Usage: toUsage(out.Usage)}, nil
}

func stopReason(c Choice) string {
	if len(c.Message.ToolCalls) > 0 || c.FinishReason == "tool_calls" {
		return "tool_use"
	}
	if c.FinishReason == "length" {
		return "max_tokens"
	}
	return "end_turn"
}

func toUsage(u *Usage) llm.Usage {
	if u == nil {
		return llm.Usage{}
	}
	return llm.Usage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens}
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
