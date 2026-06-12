package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/retry"
	"github.com/owainlewis/neo/internal/logx"
)

const defaultEndpoint = "https://api.openai.com/v1/responses"

// Client talks to the OpenAI Responses API using API-key authentication.
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

func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	apiReq := buildAPIRequest(req, model, false, "", false)
	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, err
	}
	logx.Debug("provider request",
		"provider", c.Name(),
		"model", model,
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
			lastErr = fmt.Errorf("openai %d: %s", status, string(raw))
			if attempt == maxRetries {
				logx.Debug("provider retries exhausted",
					"provider", c.Name(),
					"status", status,
					"body", logx.PayloadValue(string(raw)),
				)
				return nil, lastErr
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
			return nil, fmt.Errorf("openai %d: %s", status, string(raw))
		}

		var out apiResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
		}
		logx.Debug("provider response",
			"provider", c.Name(),
			"status", status,
			"response", logx.PayloadValue(string(raw)),
			"items", len(out.Output),
		)
		return toResponse(out)
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
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, 0, retry.Absent(), err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, retry.ParseRetryAfterHeader(resp.Header.Get("Retry-After"), time.Now()), nil
}
