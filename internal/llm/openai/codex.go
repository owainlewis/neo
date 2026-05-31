package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

// codexEndpoint is the ChatGPT/Codex subscription backend. It speaks the same
// Responses shape as the public API but at a different host, requires OAuth
// bearer auth plus a chatgpt-account-id header, and streams results over SSE.
const codexEndpoint = "https://chatgpt.com/backend-api/codex/responses"

// DefaultCodexModel is the model used for subscription requests when the config
// omits one. The Codex backend accepts the Codex-tuned model ids; override via
// the `model:` config key if needed.
const DefaultCodexModel = "gpt-5-codex"

// CredentialSource yields a valid subscription access token and its associated
// ChatGPT account id, refreshing as needed. It is satisfied by
// auth.TokenSource (via a thin adapter in the command layer).
type CredentialSource interface {
	Token(ctx context.Context) (accessToken, accountID string, err error)
}

// CodexClient talks to the ChatGPT/Codex subscription backend using OAuth
// credentials from a CredentialSource.
type CodexClient struct {
	Source     CredentialSource
	Endpoint   string
	HTTP       *http.Client
	MaxRetries int           // default: 4
	BaseDelay  time.Duration // default: 500ms
}

// NewCodex builds a subscription client backed by src.
func NewCodex(src CredentialSource) *CodexClient {
	return &CodexClient{
		Source:     src,
		Endpoint:   codexEndpoint,
		HTTP:       &http.Client{Timeout: 5 * time.Minute},
		MaxRetries: 4,
		BaseDelay:  500 * time.Millisecond,
	}
}

func (c *CodexClient) Name() string { return "openai-codex" }

func (c *CodexClient) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	model := req.Model
	if model == "" {
		model = DefaultCodexModel
	}
	apiReq := buildAPIRequest(req, model, true, "auto", true)
	debugJSON("codex request", apiReq)
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
			if err := sleep(ctx, backoffDelay(baseDelay, attempt)); err != nil {
				return nil, err
			}
			continue
		}

		if status == 429 || status >= 500 {
			lastErr = fmt.Errorf("openai-codex %d: %s", status, string(raw))
			if attempt == maxRetries {
				return nil, lastErr
			}
			if err := sleep(ctx, delayWithHeader(baseDelay, attempt, retryAfter)); err != nil {
				return nil, err
			}
			continue
		}
		if status >= 400 {
			debugHTTPResponse("openai-codex", status, raw)
			return nil, fmt.Errorf("openai-codex %d: %s", status, string(raw))
		}

		return parseCodexStream(raw)
	}
	return nil, lastErr
}

// doRequest fetches a fresh token (refreshing if needed), issues one POST, and
// returns the buffered body, status, and any Retry-After hint. The SSE body is
// small enough to buffer fully — neo presents blocking results, so there is no
// need to consume deltas incrementally.
func (c *CodexClient) doRequest(ctx context.Context, body []byte) ([]byte, int, time.Duration, error) {
	access, accountID, err := c.Source.Token(ctx)
	if err != nil {
		return nil, 0, 0, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+access)
	httpReq.Header.Set("chatgpt-account-id", accountID)
	httpReq.Header.Set("originator", "neo")
	httpReq.Header.Set("OpenAI-Beta", "responses=experimental")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, 0, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, parseRetryAfterHeader(resp.Header.Get("Retry-After")), nil
}

// parseCodexStream extracts the final result from a Responses SSE stream.
//
// The output items are assembled from the per-item "response.output_item.done"
// events, each of which carries one completed output item (a message, a
// function_call, etc.). The terminal "response.completed" event is used for
// status and usage — the ChatGPT/Codex backend omits the `output` array from
// it, so we must not rely on it for content. As a fallback, a "response.completed"
// that does include output (the public API shape) is honored, and a non-SSE
// body is parsed directly.
func parseCodexStream(raw []byte) (*llm.Response, error) {
	if !bytes.Contains(raw, []byte("data:")) {
		var out apiResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode: %w (body: %s)", err, string(raw))
		}
		return toResponse(out)
	}

	var completed *apiResponse
	var items []outputItem
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "" || data == "[DONE]" {
			continue
		}

		var ev struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
			Item     json.RawMessage `json:"item"`
			Error    *responseError  `json:"error"`
			Message  string          `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue // ignore malformed event lines
		}

		switch ev.Type {
		case "response.output_item.done":
			// Each done event carries one fully-assembled output item; this is
			// the authoritative source of content on the Codex backend.
			var item outputItem
			if err := json.Unmarshal(ev.Item, &item); err == nil {
				item.Raw = append(item.Raw[:0], ev.Item...)
				items = append(items, item)
			}
		case "response.completed", "response.incomplete":
			var out apiResponse
			if err := json.Unmarshal(ev.Response, &out); err != nil {
				return nil, fmt.Errorf("decode %s: %w", ev.Type, err)
			}
			completed = &out
		case "response.failed", "error":
			if ev.Error != nil && ev.Error.Message != "" {
				return nil, fmt.Errorf("openai-codex: %s", ev.Error.Message)
			}
			if ev.Message != "" {
				return nil, fmt.Errorf("openai-codex: %s", ev.Message)
			}
			// response.failed may nest the error inside the response object.
			if len(ev.Response) > 0 {
				var out apiResponse
				if err := json.Unmarshal(ev.Response, &out); err == nil && out.Error != nil {
					return nil, fmt.Errorf("openai-codex: %s", out.Error.Message)
				}
			}
			return nil, fmt.Errorf("openai-codex: request failed")
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read codex stream: %w", err)
	}
	if completed == nil {
		return nil, fmt.Errorf("openai-codex: stream ended without a completed response")
	}
	// The Codex backend leaves `output` empty on response.completed; fill it
	// from the per-item done events. If the server did include output (public
	// API shape), prefer it.
	if len(completed.Output) == 0 {
		completed.Output = items
	}
	return toResponse(*completed)
}
