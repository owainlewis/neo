package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/retry"
	"github.com/owainlewis/neo/internal/logx"
)

const defaultEndpoint = "https://api.anthropic.com/v1/messages"
const defaultVersion = "2023-06-01"

type Client struct {
	APIKey     string
	Endpoint   string
	Version    string
	HTTP       *http.Client
	MaxRetries int           // default: 4
	BaseDelay  time.Duration // default: 500ms
}

func New() (*Client, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	return &Client{
		APIKey:     key,
		Endpoint:   defaultEndpoint,
		Version:    defaultVersion,
		HTTP:       &http.Client{Timeout: 5 * time.Minute},
		MaxRetries: 4,
		BaseDelay:  500 * time.Millisecond,
	}, nil
}

func (c *Client) Name() string { return "anthropic" }

type apiRequest struct {
	Model     string         `json:"model"`
	System    any            `json:"system,omitempty"` // string or []systemBlock
	Messages  []llm.Message  `json:"messages"`
	Tools     []llm.ToolSpec `json:"tools,omitempty"`
	MaxTokens int            `json:"max_tokens"`
}

// systemBlock is an Anthropic system content block. A non-nil CacheControl marks
// a prompt-cache breakpoint: this block and everything before it are cached.
type systemBlock struct {
	Type         string        `json:"type"` // always "text"
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

type apiResponse struct {
	Content    []llm.ContentBlock `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      *struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// systemPayload renders the request's system prompt for the API. When the
// request carries SystemBlocks it emits a content-block array, attaching a
// cache_control breakpoint to each block flagged for caching; otherwise it
// falls back to the plain System string.
func systemPayload(req llm.Request) any {
	if len(req.SystemBlocks) == 0 {
		return req.System
	}
	blocks := make([]systemBlock, 0, len(req.SystemBlocks))
	for _, b := range req.SystemBlocks {
		if b.Text == "" {
			continue
		}
		blk := systemBlock{Type: "text", Text: b.Text}
		if b.Cache {
			blk.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		blocks = append(blocks, blk)
	}
	if len(blocks) == 0 {
		return nil
	}
	return blocks
}

// wireMessages strips content blocks the Messages API does not accept — for
// example "raw" reasoning items persisted by the OpenAI provider, which would
// otherwise 400 when a session is resumed under this provider. Messages left
// with no content are dropped entirely.
func wireMessages(in []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(in))
	for _, m := range in {
		blocks := make([]llm.ContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			switch b.Type {
			case "text", "tool_use", "tool_result", "image":
				blocks = append(blocks, b)
			}
		}
		if len(blocks) == 0 {
			continue
		}
		out = append(out, llm.Message{Role: m.Role, Content: blocks})
	}
	return out
}

func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if req.MaxTokens == 0 {
		req.MaxTokens = 8192
	}
	body, err := json.Marshal(apiRequest{
		Model:     req.Model,
		System:    systemPayload(req),
		Messages:  wireMessages(req.Messages),
		Tools:     req.Tools,
		MaxTokens: req.MaxTokens,
	})
	if err != nil {
		return nil, err
	}
	logx.Debug("provider request",
		"provider", c.Name(),
		"model", req.Model,
		"messages", len(req.Messages),
		"tools", len(req.Tools),
		"payload", logx.PayloadValue(string(body)),
	)

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
		logx.Debug("provider attempt", "provider", c.Name(), "attempt", attempt+1, "max_attempts", maxRetries+1)
		raw, status, retryAfter, err := c.doRequest(ctx, body)
		if err != nil {
			// Network errors: retry unless the context is done.
			lastErr = err
			if ctx.Err() != nil {
				logx.Debug("provider request canceled", "provider", c.Name(), "error", ctx.Err().Error())
				return nil, ctx.Err()
			}
			if attempt == maxRetries {
				logx.Debug("provider transport failed", "provider", c.Name(), "attempt", attempt+1, "error", err.Error())
				return nil, err
			}
			delay := retry.Delay(baseDelay, attempt, retry.Absent())
			logx.Debug("provider retry scheduled",
				"provider", c.Name(),
				"attempt", attempt+1,
				"reason", "transport_error",
				"delay", delay.String(),
				"error", err.Error(),
			)
			if err := sleep(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}

		if status == 429 || status >= 500 {
			lastErr = fmt.Errorf("anthropic %d: %s", status, string(raw))
			if attempt == maxRetries {
				logx.Debug("provider retries exhausted",
					"provider", c.Name(),
					"status", status,
					"body", logx.PayloadValue(string(raw)),
				)
				return nil, lastErr
			}
			if !retryAfter.Present {
				retryAfter = parseRetryAfterBody(raw)
			}
			delay := retry.Delay(baseDelay, attempt, retryAfter)
			logx.Debug("provider retry scheduled",
				"provider", c.Name(),
				"attempt", attempt+1,
				"reason", "http_retryable",
				"status", status,
				"delay", delay.String(),
				"retry_after_present", retryAfter.Present,
				"body", logx.PayloadValue(string(raw)),
			)
			if err := sleep(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}
		if status >= 400 {
			logx.Debug("provider client error",
				"provider", c.Name(),
				"status", status,
				"body", logx.PayloadValue(string(raw)),
			)
			return nil, fmt.Errorf("anthropic %d: %s", status, string(raw))
		}

		var out apiResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
		}
		if out.Error != nil {
			return nil, fmt.Errorf("anthropic: %s", out.Error.Message)
		}
		logx.Debug("provider response",
			"provider", c.Name(),
			"status", status,
			"response", logx.PayloadValue(string(raw)),
			"items", len(out.Content),
			"stop_reason", out.StopReason,
		)
		resp := &llm.Response{Content: out.Content, StopReason: out.StopReason}
		if out.Usage != nil {
			resp.Usage = llm.Usage{
				InputTokens:         out.Usage.InputTokens,
				OutputTokens:        out.Usage.OutputTokens,
				CacheCreationTokens: out.Usage.CacheCreationInputTokens,
				CacheReadTokens:     out.Usage.CacheReadInputTokens,
			}
		}
		return resp, nil
	}
	return nil, lastErr
}

// doRequest issues one POST and returns the body, status, and any Retry-After
// hint from the response header.
func (c *Client) doRequest(ctx context.Context, body []byte) ([]byte, int, retry.RetryAfter, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, retry.Absent(), err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", c.Version)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, 0, retry.Absent(), err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, retry.ParseRetryAfterHeader(resp.Header.Get("Retry-After"), time.Now()), nil
}

func parseRetryAfterBody(body []byte) retry.RetryAfter {
	var probe struct {
		RetryAfter any `json:"retry_after"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return retry.Absent()
	}
	switch v := probe.RetryAfter.(type) {
	case float64:
		if v < 0 {
			return retry.Absent()
		}
		return retry.RetryAfter{Delay: time.Duration(v * float64(time.Second)), Present: true}
	case string:
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return retry.Absent()
		}
		return retry.RetryAfter{Delay: time.Duration(n) * time.Second, Present: true}
	}
	return retry.Absent()
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
