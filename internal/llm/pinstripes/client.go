// Package pinstripes implements the llm.Provider interface against the
// Pinstripes OpenAI-compatible Chat Completions API (https://pinstripes.io).
//
// Pinstripes exposes OpenAI Chat Completions at:
//
//	https://api.pinstripes.io/v1/chat/completions
//
// Auth is a standard Bearer token from PINSTRIPES_API_KEY.
// Prefix caching is applied automatically at 0.5% of input token price.
package pinstripes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

const (
	DefaultEndpoint = "https://api.pinstripes.io/v1/chat/completions"
	DefaultModel    = "deepseek-v4-flash"
)

// Client talks to the Pinstripes Chat Completions API.
type Client struct {
	APIKey   string
	Endpoint string
	HTTP     *http.Client
}

// New constructs a Client from the PINSTRIPES_API_KEY environment variable.
func New() (*Client, error) {
	key := os.Getenv("PINSTRIPES_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("PINSTRIPES_API_KEY is not set")
	}
	return &Client{
		APIKey:   key,
		Endpoint: DefaultEndpoint,
		HTTP:     &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

func (c *Client) Name() string { return "pinstripes" }

func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = DefaultModel
	}

	apiReq := buildRequest(req, model)
	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("pinstripes %d: %s", resp.StatusCode, string(raw))
	}

	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("pinstripes decode: %w (body: %s)", err, string(raw))
	}
	return toResponse(out)
}

// --- wire types -------------------------------------------------------------

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	Tools     []chatTool    `json:"tools,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall  `json:"tool_calls,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatTool struct {
	Type     string       `json:"type"` // "function"
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage,omitempty"`
}

type chatChoice struct {
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// --- translation: llm.Request -> Chat Completions ---------------------------

func buildRequest(req llm.Request, model string) chatRequest {
	var msgs []chatMessage

	// System prompt.
	sysText := systemText(req)
	if sysText != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: sysText})
	}

	for _, m := range req.Messages {
		switch m.Role {
		case llm.RoleUser, llm.RoleTool:
			for _, b := range m.Content {
				switch b.Type {
				case "text": // plain user text
					msgs = append(msgs, chatMessage{Role: "user", Content: b.Text})
				case "tool_result": // tool output
					msgs = append(msgs, chatMessage{
						Role:       "tool",
						Content:    b.Content,
						ToolCallID: b.ToolUseID,
					})
				}
			}
		case llm.RoleAssistant:
			var text strings.Builder
			var calls []toolCall
			for _, b := range m.Content {
				switch b.Type {
				case "text": // assistant text
					text.WriteString(b.Text)
				case "tool_use": // tool call
					args, _ := json.Marshal(b.Input)
					calls = append(calls, toolCall{
						ID:       b.ID,
						Type:     "function",
						Function: toolFunction{Name: b.Name, Arguments: string(args)},
					})
				}
			}
			msg := chatMessage{Role: "assistant"}
			if text.Len() > 0 {
				msg.Content = text.String()
			}
			if len(calls) > 0 {
				msg.ToolCalls = calls
			}
			msgs = append(msgs, msg)
		}
	}

	var tools []chatTool
	for _, t := range req.Tools {
		tools = append(tools, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	return chatRequest{
		Model:     model,
		Messages:  msgs,
		Tools:     tools,
		MaxTokens: req.MaxTokens,
	}
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

// --- translation: Chat Completions -> llm.Response --------------------------

func toResponse(out chatResponse) (*llm.Response, error) {
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("pinstripes: empty choices in response")
	}
	choice := out.Choices[0]
	msg := choice.Message

	var content []llm.ContentBlock
	if s, ok := msg.Content.(string); ok && s != "" {
		content = append(content, llm.ContentBlock{Type: "text", Text: s})
	}
	for _, tc := range msg.ToolCalls {
		input := map[string]any{}
		if tc.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		}
		content = append(content, llm.ContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	stopReason := "end_turn"
	if choice.FinishReason == "tool_calls" {
		stopReason = "tool_use"
	} else if choice.FinishReason == "length" {
		stopReason = "max_tokens"
	}

	var usage llm.Usage
	if out.Usage != nil {
		usage.InputTokens = out.Usage.PromptTokens
		usage.OutputTokens = out.Usage.CompletionTokens
	}

	return &llm.Response{
		Content:    content,
		StopReason: stopReason,
		Usage:      usage,
	}, nil
}
