package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

type stubSource struct {
	token   string
	account string
}

func (s stubSource) Token(context.Context) (string, string, error) {
	return s.token, s.account, nil
}

func newTestCodex(srv *httptest.Server) *CodexClient {
	return &CodexClient{
		Source:     stubSource{token: "tok", account: "acct_1"},
		Endpoint:   srv.URL,
		HTTP:       srv.Client(),
		MaxRetries: 3,
		BaseDelay:  1 * time.Millisecond,
	}
}

// codexCompletedStream mirrors the real ChatGPT/Codex backend: content arrives
// in per-item "response.output_item.done" events, and the terminal
// "response.completed" event carries status and usage but NO output array.
const codexCompletedStream = `event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"hel"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"bash","arguments":"{\"cmd\":\"ls\"}"}}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":5,"output_tokens":3,"input_tokens_details":{"cached_tokens":2}}}}

data: [DONE]
`

func TestCodex_StreamAssembly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth header: got %q", got)
		}
		if got := r.Header.Get("chatgpt-account-id"); got != "acct_1" {
			t.Errorf("account header: got %q", got)
		}
		if got := r.Header.Get("originator"); got != "neo" {
			t.Errorf("originator: got %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(codexCompletedStream))
	}))
	defer srv.Close()

	resp, err := newTestCodex(srv).Complete(context.Background(), llm.Request{
		Model:  "gpt-5-codex",
		System: "be brief",
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Fatalf("stop reason: got %q want tool_use", resp.StopReason)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %+v", resp.Content)
	}
	if resp.Content[0].Type != "text" || resp.Content[0].Text != "hello" {
		t.Fatalf("bad text block: %+v", resp.Content[0])
	}
	tc := resp.Content[1]
	if tc.Type != "tool_use" || tc.ID != "call_1" || tc.Name != "bash" || tc.Input["cmd"] != "ls" {
		t.Fatalf("bad tool_use block: %+v", tc)
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 3 || resp.Usage.CacheReadTokens != 2 {
		t.Fatalf("bad usage: %+v", resp.Usage)
	}
}

func TestCodex_StreamPreservesRawReasoning(t *testing.T) {
	raw := []byte(`event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"secret"}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_1","name":"bash","arguments":"{\"cmd\":\"ls\"}"}}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed"}}

data: [DONE]
`)

	resp, err := parseCodexStream(raw)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("expected reasoning + tool use, got %+v", resp.Content)
	}
	if resp.Content[0].Type != "raw" || string(resp.Content[0].Raw) != `{"type":"reasoning","id":"rs_1","encrypted_content":"secret"}` {
		t.Fatalf("reasoning raw item not preserved: %+v", resp.Content[0])
	}
	if resp.Content[1].Type != "tool_use" || resp.Content[1].ID != "call_1" {
		t.Fatalf("tool call not preserved: %+v", resp.Content[1])
	}
}

func TestCodex_StreamIncompleteReturnsMaxTokens(t *testing.T) {
	raw := []byte(`event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}}

event: response.incomplete
data: {"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":4,"output_tokens":2}}}

data: [DONE]
`)

	resp, err := parseCodexStream(raw)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.StopReason != "max_tokens" {
		t.Fatalf("stop reason: got %q want max_tokens", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "partial" {
		t.Fatalf("partial output was not preserved: %+v", resp.Content)
	}
	if resp.Usage.InputTokens != 4 || resp.Usage.OutputTokens != 2 {
		t.Fatalf("usage not preserved: %+v", resp.Usage)
	}
}

func TestCodex_StreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: error\ndata: {\"type\":\"error\",\"message\":\"boom\"}\n\n"))
	}))
	defer srv.Close()

	_, err := newTestCodex(srv).Complete(context.Background(), llm.Request{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected boom error, got %v", err)
	}
}

func TestCodex_RetriesOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexCompletedStream))
	}))
	defer srv.Close()

	resp, err := newTestCodex(srv).Complete(context.Background(), llm.Request{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.Content[0].Text != "hello" {
		t.Fatalf("bad response after retries: %+v", resp)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}
