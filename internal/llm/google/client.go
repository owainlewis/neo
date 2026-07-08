// Package google implements the llm.Provider interface against the Google
// Gemini GenerateContent API.
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/retry"
	"github.com/owainlewis/neo/internal/logx"
)

const (
	DefaultEndpoint = "https://generativelanguage.googleapis.com/v1beta/models"
	DefaultModel    = "gemini-2.5-pro"
)

// Client talks to Google's Gemini GenerateContent API.
type Client struct {
	APIKey     string
	Endpoint   string
	HTTP       *http.Client
	MaxRetries int
	BaseDelay  time.Duration
}

// New constructs a Gemini provider from GOOGLE_API_KEY.
func New() (*Client, error) {
	key := os.Getenv("GOOGLE_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("GOOGLE_API_KEY is not set")
	}
	return &Client{
		APIKey:     key,
		Endpoint:   DefaultEndpoint,
		HTTP:       &http.Client{Timeout: 5 * time.Minute},
		MaxRetries: 4,
		BaseDelay:  500 * time.Millisecond,
	}, nil
}

func (c *Client) Name() string { return "google" }

func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	apiReq := buildRequest(req)
	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, err
	}
	logx.Debug("provider request", "provider", c.Name(), "model", model, "messages", len(req.Messages), "tools", len(req.Tools), "payload", logx.PayloadValue(string(body)))

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
		raw, status, retryAfter, err := c.doRequest(ctx, model, body)
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
			lastErr = fmt.Errorf("google %d: %s", status, string(raw))
			if attempt == maxRetries {
				return nil, lastErr
			}
			if err := sleep(ctx, retry.Delay(baseDelay, attempt, retryAfter)); err != nil {
				return nil, err
			}
			continue
		}
		if status >= 400 {
			return nil, fmt.Errorf("google %d: %s", status, string(raw))
		}
		var out response
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
		}
		logx.Debug("provider response", "provider", c.Name(), "status", status, "response", logx.PayloadValue(string(raw)), "candidates", len(out.Candidates))
		return toLLMResponse(out)
	}
	return nil, lastErr
}

func (c *Client) doRequest(ctx context.Context, model string, body []byte) ([]byte, int, retry.RetryAfter, error) {
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	endpoint := strings.TrimRight(c.Endpoint, "/") + "/" + url.PathEscape(model) + ":generateContent"
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, 0, retry.Absent(), err
	}
	q := u.Query()
	q.Set("key", c.APIKey)
	u.RawQuery = q.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, 0, retry.Absent(), err
	}
	httpReq.Header.Set("Content-Type", "application/json")
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

type request struct {
	SystemInstruction *content          `json:"systemInstruction,omitempty"`
	Contents          []content         `json:"contents"`
	Tools             []tool            `json:"tools,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

type generationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *inlineData       `json:"inlineData,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
}

type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type functionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type functionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type tool struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations"`
}

type functionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type response struct {
	Candidates []candidate `json:"candidates"`
	Usage      *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type candidate struct {
	Content      content `json:"content"`
	FinishReason string  `json:"finishReason"`
}

func buildRequest(req llm.Request) request {
	out := request{Contents: toContents(req.Messages), Tools: toTools(req.Tools)}
	if system := systemText(req); system != "" {
		out.SystemInstruction = &content{Parts: []part{{Text: system}}}
	}
	if req.MaxTokens > 0 {
		out.GenerationConfig = &generationConfig{MaxOutputTokens: req.MaxTokens}
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

func toContents(messages []llm.Message) []content {
	out := make([]content, 0, len(messages))
	toolNames := map[string]string{}
	for _, m := range messages {
		parts := toParts(m.Content, toolNames)
		if len(parts) == 0 {
			continue
		}
		role := "user"
		if m.Role == llm.RoleAssistant {
			role = "model"
		}
		out = append(out, content{Role: role, Parts: parts})
	}
	return out
}

func toParts(blocks []llm.ContentBlock, toolNames map[string]string) []part {
	parts := make([]part, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, part{Text: b.Text})
			}
		case "image":
			if b.Source != nil && b.Source.Data != "" {
				mt := b.Source.MediaType
				if mt == "" {
					mt = "application/octet-stream"
				}
				parts = append(parts, part{InlineData: &inlineData{MimeType: mt, Data: b.Source.Data}})
			}
		case "tool_use":
			if b.ID != "" && b.Name != "" {
				toolNames[b.ID] = b.Name
			}
			parts = append(parts, part{FunctionCall: &functionCall{ID: b.ID, Name: b.Name, Args: b.Input}})
		case "tool_result":
			name := b.Name
			if name == "" {
				name = toolNames[b.ToolUseID]
			}
			if name == "" {
				name = b.ToolUseID
			}
			parts = append(parts, part{FunctionResponse: &functionResponse{ID: b.ToolUseID, Name: name, Response: map[string]any{"content": b.Content, "is_error": b.IsError}}})
		}
	}
	return parts
}

func toTools(specs []llm.ToolSpec) []tool {
	if len(specs) == 0 {
		return nil
	}
	decls := make([]functionDeclaration, 0, len(specs))
	for _, s := range specs {
		decls = append(decls, functionDeclaration{Name: s.Name, Description: s.Description, Parameters: s.InputSchema})
	}
	return []tool{{FunctionDeclarations: decls}}
}

func toLLMResponse(out response) (*llm.Response, error) {
	if out.Error != nil {
		return nil, fmt.Errorf("google: %s", out.Error.Message)
	}
	if len(out.Candidates) == 0 {
		return nil, fmt.Errorf("google: no candidates returned")
	}
	cand := out.Candidates[0]
	blocks := make([]llm.ContentBlock, 0, len(cand.Content.Parts))
	for _, p := range cand.Content.Parts {
		if p.Text != "" {
			blocks = append(blocks, llm.ContentBlock{Type: "text", Text: p.Text})
		}
		if p.FunctionCall != nil {
			id := p.FunctionCall.ID
			if id == "" {
				id = p.FunctionCall.Name
			}
			input := p.FunctionCall.Args
			if input == nil {
				input = map[string]any{}
			}
			blocks = append(blocks, llm.ContentBlock{Type: "tool_use", ID: id, Name: p.FunctionCall.Name, Input: input})
		}
	}
	resp := &llm.Response{Content: blocks, StopReason: stopReason(cand)}
	if out.Usage != nil {
		resp.Usage = llm.Usage{InputTokens: out.Usage.PromptTokenCount, OutputTokens: out.Usage.CandidatesTokenCount}
	}
	return resp, nil
}

func stopReason(c candidate) string {
	for _, p := range c.Content.Parts {
		if p.FunctionCall != nil {
			return "tool_use"
		}
	}
	if c.FinishReason == "MAX_TOKENS" {
		return "max_tokens"
	}
	return "end_turn"
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
