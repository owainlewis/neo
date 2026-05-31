package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

func newTestClient(srv *httptest.Server) *Client {
	return &Client{
		APIKey:     "test",
		Endpoint:   srv.URL,
		HTTP:       srv.Client(),
		MaxRetries: 3,
		BaseDelay:  1 * time.Millisecond,
	}
}

func TestComplete_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test" {
			t.Errorf("missing/incorrect auth header, got %q", got)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("stop reason: got %q want end_turn", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "hi" {
		t.Fatalf("bad content: %+v", resp.Content)
	}
	if resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 1 {
		t.Fatalf("bad usage: %+v", resp.Usage)
	}
}

func TestComplete_ToolCallRoundTrip(t *testing.T) {
	var captured apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(200)
		w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"cmd\":\"ls\"}"}}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer srv.Close()

	req := llm.Request{
		Model:  "m",
		System: "you are neo",
		Tools: []llm.ToolSpec{{
			Name:        "bash",
			Description: "run a command",
			InputSchema: map[string]any{"type": "object"},
		}},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "list files"}}},
		},
	}
	resp, err := newTestClient(srv).Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	// Request translation: system + user message, and one function tool.
	if len(captured.Messages) != 2 || captured.Messages[0].Role != "system" || captured.Messages[1].Role != "user" {
		t.Fatalf("bad outgoing messages: %+v", captured.Messages)
	}
	if len(captured.Tools) != 1 || captured.Tools[0].Function.Name != "bash" {
		t.Fatalf("bad outgoing tools: %+v", captured.Tools)
	}

	// Response translation: tool_calls -> tool_use + stop reason mapping.
	if resp.StopReason != "tool_use" {
		t.Fatalf("stop reason: got %q want tool_use", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "tool_use" {
		t.Fatalf("bad content: %+v", resp.Content)
	}
	b := resp.Content[0]
	if b.ID != "call_1" || b.Name != "bash" || b.Input["cmd"] != "ls" {
		t.Fatalf("bad tool_use block: %+v", b)
	}
}

func TestToAPIMessages_ToolResultBecomesToolRole(t *testing.T) {
	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: "tool_use", ID: "call_1", Name: "bash", Input: map[string]any{"cmd": "ls"}},
			}},
			{Role: llm.RoleUser, Content: []llm.ContentBlock{
				{Type: "tool_result", ToolUseID: "call_1", Content: "file.txt"},
			}},
		},
	}
	msgs := toAPIMessages(req)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "assistant" || len(msgs[0].ToolCalls) != 1 || msgs[0].ToolCalls[0].ID != "call_1" {
		t.Fatalf("bad assistant message: %+v", msgs[0])
	}
	if msgs[1].Role != "tool" || msgs[1].ToolCallID != "call_1" || msgs[1].Content != "file.txt" {
		t.Fatalf("bad tool message: %+v", msgs[1])
	}
}

func TestSystemText_FlattensBlocks(t *testing.T) {
	req := llm.Request{SystemBlocks: []llm.SystemBlock{
		{Text: "stable", Cache: true},
		{Text: ""},
		{Text: "dynamic"},
	}}
	if got, want := systemText(req), "stable\n\ndynamic"; got != want {
		t.Fatalf("systemText: got %q want %q", got, want)
	}
}

func TestComplete_RetriesOn5xxThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(503)
			w.Write([]byte(`{"error":{"message":"overloaded"}}`))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.Content[0].Text != "ok" {
		t.Fatalf("bad response after retries: %+v", resp)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestComplete_4xxNoRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m"})
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if calls != 1 {
		t.Fatalf("expected no retries on 4xx, got %d calls", calls)
	}
}

func TestMapFinishReason(t *testing.T) {
	cases := map[string]string{
		"tool_calls": "tool_use",
		"stop":       "end_turn",
		"length":     "max_tokens",
		"other":      "other",
	}
	for in, want := range cases {
		if got := mapFinishReason(in); got != want {
			t.Errorf("mapFinishReason(%q): got %q want %q", in, got, want)
		}
	}
}
