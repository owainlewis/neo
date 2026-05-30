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

func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if req.MaxTokens == 0 {
		req.MaxTokens = 8192
	}
	body, err := json.Marshal(apiRequest{
		Model:     req.Model,
		System:    systemPayload(req),
		Messages:  req.Messages,
		Tools:     req.Tools,
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
		raw, status, err := c.doRequest(ctx, body)
		if err != nil {
			// Network errors: retry unless the context is done.
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt == maxRetries {
				return nil, err
			}
			if err := sleep(ctx, backoffDelay(baseDelay, attempt, "")); err != nil {
				return nil, err
			}
			continue
		}

		if status == 429 || status >= 500 {
			lastErr = fmt.Errorf("anthropic %d: %s", status, string(raw))
			if attempt == maxRetries {
				return nil, lastErr
			}
			retryAfter := parseRetryAfter(raw, status)
			if err := sleep(ctx, backoffDelayFromHeader(baseDelay, attempt, retryAfter)); err != nil {
				return nil, err
			}
			continue
		}
		if status >= 400 {
			return nil, fmt.Errorf("anthropic %d: %s", status, string(raw))
		}

		var out apiResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
		}
		if out.Error != nil {
			return nil, fmt.Errorf("anthropic: %s", out.Error.Message)
		}
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

// doRequest issues one POST and returns the body and status.
func (c *Client) doRequest(ctx context.Context, body []byte) ([]byte, int, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", c.Version)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, nil
}

func backoffDelay(base time.Duration, attempt int, _ string) time.Duration {
	d := base << attempt // 500ms, 1s, 2s, 4s, ...
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

func backoffDelayFromHeader(base time.Duration, attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	return backoffDelay(base, attempt, "")
}

// parseRetryAfter extracts a Retry-After hint. The Anthropic API surfaces this
// inside the response body for 429s; we look for a top-level "retry-after"-ish
// field, otherwise return 0 and fall back to exponential backoff.
func parseRetryAfter(body []byte, _ int) time.Duration {
	var probe struct {
		RetryAfter any `json:"retry_after"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return 0
	}
	switch v := probe.RetryAfter.(type) {
	case float64:
		return time.Duration(v * float64(time.Second))
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
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
