package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

func newTestClient(srv *httptest.Server) *Client {
	return &Client{APIKey: "test-key", Endpoint: srv.URL, HTTP: srv.Client(), MaxRetries: 0, BaseDelay: time.Millisecond}
}

func TestNew_UsesAPIKeyFromEnvironment(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-env-key")
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.APIKey != "test-env-key" {
		t.Fatalf("APIKey = %q", c.APIKey)
	}
	if c.Name() != "google" {
		t.Fatalf("Name = %q", c.Name())
	}
}

func TestNew_MissingAPIKey(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	_, err := New()
	if err == nil {
		t.Fatal("expected missing GOOGLE_API_KEY error")
	}
	if !strings.Contains(err.Error(), "GOOGLE_API_KEY") {
		t.Fatalf("error should mention GOOGLE_API_KEY, got %v", err)
	}
}

func TestComplete_BuildsRequestWithSystemPromptMessagesAndTools(t *testing.T) {
	var captured request
	var path, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		key = r.URL.Query().Get("key")
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content-type = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()

	req := llm.Request{
		Model:     "gemini-test",
		System:    "you are neo",
		MaxTokens: 123,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hello"}}}},
		Tools: []llm.ToolSpec{{Name: "bash", Description: "run shell", InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"cmd": map[string]any{"type": "string"}},
		}}},
	}
	if _, err := newTestClient(srv).Complete(context.Background(), req); err != nil {
		t.Fatalf("complete: %v", err)
	}

	if path != "/gemini-test:generateContent" {
		t.Fatalf("path = %q", path)
	}
	if key != "test-key" {
		t.Fatalf("api key query = %q", key)
	}
	if captured.SystemInstruction == nil || captured.SystemInstruction.Parts[0].Text != "you are neo" {
		t.Fatalf("bad system instruction: %+v", captured.SystemInstruction)
	}
	if len(captured.Contents) != 1 || captured.Contents[0].Role != "user" || captured.Contents[0].Parts[0].Text != "hello" {
		t.Fatalf("bad contents: %+v", captured.Contents)
	}
	if captured.GenerationConfig == nil || captured.GenerationConfig.MaxOutputTokens != 123 {
		t.Fatalf("bad generation config: %+v", captured.GenerationConfig)
	}
	if len(captured.Tools) != 1 || len(captured.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("bad tools: %+v", captured.Tools)
	}
	decl := captured.Tools[0].FunctionDeclarations[0]
	if decl.Name != "bash" || decl.Description != "run shell" || decl.Parameters["type"] != "object" {
		t.Fatalf("bad function declaration: %+v", decl)
	}
}

func TestComplete_ParsesTextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":2}}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "gemini-test"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if resp.StopReason != "end_turn" || len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "hi" {
		t.Fatalf("bad response: %+v", resp)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 2 {
		t.Fatalf("bad usage: %+v", resp.Usage)
	}
}

func TestComplete_ParsesToolCallResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"bash","args":{"cmd":"ls"}}}]},"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "gemini-test"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if resp.StopReason != "tool_use" || len(resp.Content) != 1 || resp.Content[0].Type != "tool_use" {
		t.Fatalf("bad response: %+v", resp)
	}
	block := resp.Content[0]
	if block.ID != "call_1" || block.Name != "bash" || block.Input["cmd"] != "ls" {
		t.Fatalf("bad tool_use: %+v", block)
	}
}

func TestComplete_SerializesToolResultContinuation(t *testing.T) {
	var captured request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]}}]}`))
	}))
	defer srv.Close()

	req := llm.Request{Model: "gemini-test", Messages: []llm.Message{
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "tool_use", ID: "call_1", Name: "bash", Input: map[string]any{"cmd": "ls"}}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "tool_result", ToolUseID: "call_1", Content: "file.txt"}}},
	}}
	if _, err := newTestClient(srv).Complete(context.Background(), req); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(captured.Contents) != 2 {
		t.Fatalf("contents = %d: %+v", len(captured.Contents), captured.Contents)
	}
	call := captured.Contents[0].Parts[0].FunctionCall
	if captured.Contents[0].Role != "model" || call == nil || call.ID != "call_1" || call.Name != "bash" || call.Args["cmd"] != "ls" {
		t.Fatalf("bad replayed function call: %+v", captured.Contents[0])
	}
	result := captured.Contents[1].Parts[0].FunctionResponse
	if captured.Contents[1].Role != "user" || result == nil || result.ID != "call_1" || result.Name != "bash" || result.Response["content"] != "file.txt" {
		t.Fatalf("bad function response: %+v", captured.Contents[1])
	}
}
