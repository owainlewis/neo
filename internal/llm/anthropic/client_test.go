package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

func newTestClient(srv *httptest.Server) *Client {
	return &Client{
		APIKey:     "test",
		Endpoint:   srv.URL,
		Version:    defaultVersion,
		HTTP:       srv.Client(),
		MaxRetries: 3,
		BaseDelay:  1 * time.Millisecond,
	}
}

func TestComplete_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test" {
			t.Errorf("missing api key header, got %q", got)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.StopReason != "end_turn" || len(resp.Content) != 1 || resp.Content[0].Text != "hi" {
		t.Fatalf("bad response: %+v", resp)
	}
}

func TestComplete_PreservesParallelCallsAndOrderedResults(t *testing.T) {
	var got apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"tool_use","id":"call_a","name":"read","input":{"path":"a"}},{"type":"tool_use","id":"call_b","name":"read","input":{"path":"b"}}],"stop_reason":"tool_use"}`))
	}))
	defer srv.Close()

	client := newTestClient(srv)
	first, err := client.Complete(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Content) != 2 || first.Content[0].ID != "call_a" || first.Content[1].ID != "call_b" {
		t.Fatalf("parallel calls = %#v", first.Content)
	}
	_, err = client.Complete(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{
		{Role: llm.RoleAssistant, Content: first.Content},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{
			{Type: "tool_result", ToolUseID: "call_a", Content: "A"},
			{Type: "tool_result", ToolUseID: "call_b", Content: "B"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	results := got.Messages[1].Content
	if len(results) != 2 || results[0].ToolUseID != "call_a" || results[1].ToolUseID != "call_b" {
		t.Fatalf("parallel results = %#v", results)
	}
}

func TestComplete_RetriesOn5xxThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(503)
			w.Write([]byte("overloaded"))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
	if resp.Content[0].Text != "ok" {
		t.Fatalf("bad response: %+v", resp)
	}
}

func TestComplete_RetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(429)
			w.Write([]byte(`{"retry_after":0}`))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m"}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestComplete_RetryAfterHeaderOverridesBodyHint(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			w.Write([]byte(`{"retry_after":30}`))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := newTestClient(srv).Complete(ctx, llm.Request{Model: "m"}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestDoRequest_ReturnsRetryAfterHeader(t *testing.T) {
	when := time.Now().UTC().Add(5 * time.Second).Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", when.Format(http.TimeFormat))
		w.WriteHeader(429)
		w.Write([]byte("slow down"))
	}))
	defer srv.Close()

	_, _, retryAfter, err := newTestClient(srv).doRequest(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !retryAfter.Present {
		t.Fatal("expected Retry-After header")
	}
	if retryAfter.Delay <= 0 || retryAfter.Delay > 5*time.Second {
		t.Fatalf("delay = %s, want within HTTP-date window", retryAfter.Delay)
	}
}

func TestComplete_DoesNotRetry4xxClientErrors(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"type":"invalid","message":"bad input"}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected 400 in error, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 attempt, got %d", got)
	}
}

func TestComplete_GivesUpAfterMaxRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.MaxRetries = 2
	_, err := c.Complete(context.Background(), llm.Request{Model: "m"})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&calls); got != 3 { // attempt 0 + 2 retries
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestComplete_SystemBlocksCarryCacheControl(t *testing.T) {
	var captured struct {
		System []struct {
			Type         string         `json:"type"`
			Text         string         `json:"text"`
			CacheControl map[string]any `json:"cache_control"`
		} `json:"system"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Complete(context.Background(), llm.Request{
		Model: "m",
		SystemBlocks: []llm.SystemBlock{
			{Text: "static base", Cache: true},
			{Text: "dynamic tail"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(captured.System) != 2 {
		t.Fatalf("expected 2 system blocks, got %d", len(captured.System))
	}
	if captured.System[0].Text != "static base" || captured.System[0].CacheControl["type"] != "ephemeral" {
		t.Fatalf("first block should be cached static base: %+v", captured.System[0])
	}
	if captured.System[1].CacheControl != nil {
		t.Fatalf("second block should not be cached: %+v", captured.System[1])
	}
}

func TestComplete_FallsBackToSystemString(t *testing.T) {
	var captured struct {
		System string `json:"system"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Complete(context.Background(), llm.Request{
		Model:  "m",
		System: "plain prompt",
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if captured.System != "plain prompt" {
		t.Fatalf("expected plain system string, got %q", captured.System)
	}
}

func TestComplete_ParsesCacheUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":7,"cache_creation_input_tokens":100,"cache_read_input_tokens":200}}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	want := llm.Usage{InputTokens: 5, OutputTokens: 7, CacheCreationTokens: 100, CacheReadTokens: 200}
	if resp.Usage != want {
		t.Fatalf("usage = %+v, want %+v", resp.Usage, want)
	}
}

func TestComplete_StripsForeignRawBlocksFromMessages(t *testing.T) {
	var captured struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string          `json:"type"`
				Raw  json.RawMessage `json:"raw"`
			} `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	// A transcript resumed from a Gemini session: neutral text and tool blocks
	// carry opaque Gemini replay metadata, plus a raw-only thought block. The
	// neutral history must survive without any foreign wire data.
	_, err := newTestClient(srv).Complete(context.Background(), llm.Request{
		Model: "m",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}},
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: "raw", Raw: json.RawMessage(`{"thought":true,"thoughtSignature":"private"}`)},
				{Type: "text", Text: "hello", Raw: json.RawMessage(`{"text":"hello","thoughtSignature":"text-signature"}`)},
				{Type: "tool_use", ID: "call_1", Name: "read", Input: map[string]any{"path": "README.md"}, Raw: json.RawMessage(`{"functionCall":{"name":"read","args":{"path":"README.md"}},"thoughtSignature":"tool-signature"}`)},
			}},
			{Role: llm.RoleUser, Content: []llm.ContentBlock{
				{Type: "tool_result", ToolUseID: "call_1", Content: "Neo"},
			}},
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{
				Type: "raw", Raw: json.RawMessage(`{"thought":true,"thoughtSignature":"tail"}`),
			}}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(captured.Messages) != 3 {
		t.Fatalf("messages sent = %d, want 3 (raw-only message dropped)", len(captured.Messages))
	}
	for _, m := range captured.Messages {
		for _, b := range m.Content {
			if b.Type == "raw" || len(b.Raw) > 0 {
				t.Fatalf("foreign raw data leaked into the Anthropic request: %+v", b)
			}
		}
	}
	if got := captured.Messages[1].Content; len(got) != 2 || got[0].Type != "text" || got[1].Type != "tool_use" {
		t.Fatalf("provider-neutral assistant history was not preserved: %+v", got)
	}
	if got := captured.Messages[2].Content; len(got) != 1 || got[0].Type != "tool_result" {
		t.Fatalf("provider-neutral tool result was not preserved: %+v", got)
	}
}

func TestComplete_MaxTokens(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request int
		want    int
	}{
		{"defaults when unset", 0, defaultMaxTokens},
		{"honors an explicit value", 4096, 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured struct {
				MaxTokens int `json:"max_tokens"`
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
					t.Errorf("decode request: %v", err)
				}
				w.WriteHeader(200)
				w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
			}))
			defer srv.Close()

			_, err := newTestClient(srv).Complete(context.Background(), llm.Request{
				Model:     "m",
				MaxTokens: tc.request,
			})
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if captured.MaxTokens != tc.want {
				t.Fatalf("max_tokens = %d, want %d", captured.MaxTokens, tc.want)
			}
		})
	}
}
