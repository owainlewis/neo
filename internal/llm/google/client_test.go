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
		key = r.Header.Get("x-goog-api-key")
		if r.URL.RawQuery != "" {
			t.Errorf("API key must not be placed in URL query: %q", r.URL.RawQuery)
		}
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
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{
			{Type: "text", Text: "hello"},
			{Type: "image", Source: &llm.ImageSource{Type: "base64", MediaType: "image/png", Data: "aW1hZ2U="}},
		}}},
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
		t.Fatalf("api key header = %q", key)
	}
	if captured.SystemInstruction == nil || captured.SystemInstruction.Parts[0].Text != "you are neo" {
		t.Fatalf("bad system instruction: %+v", captured.SystemInstruction)
	}
	if len(captured.Contents) != 1 || captured.Contents[0].Role != "user" || captured.Contents[0].Parts[0].Text != "hello" {
		t.Fatalf("bad contents: %+v", captured.Contents)
	}
	image := captured.Contents[0].Parts[1].InlineData
	if image == nil || image.MimeType != "image/png" || image.Data != "aW1hZ2U=" {
		t.Fatalf("bad inline image: %+v", image)
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
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":2,"thoughtsTokenCount":3,"cachedContentTokenCount":4}}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "gemini-test"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if resp.StopReason != "end_turn" || len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "hi" {
		t.Fatalf("bad response: %+v", resp)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 5 || resp.Usage.CacheReadTokens != 4 {
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
	if captured.Contents[1].Role != "user" || result == nil || result.ID != "call_1" || result.Name != "bash" || result.Response["output"] != "file.txt" {
		t.Fatalf("bad function response: %+v", captured.Contents[1])
	}
}

func TestComplete_PreservesThoughtSignatureAcrossToolContinuation(t *testing.T) {
	var requests []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var captured request
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests = append(requests, captured)
		w.WriteHeader(http.StatusOK)
		if len(requests) == 1 {
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"bash","args":{"cmd":"pwd"}},"thoughtSignature":"opaque-signature"}]},"finishReason":"STOP"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()

	client := newTestClient(srv)
	first, err := client.Complete(context.Background(), llm.Request{Model: "gemini-test"})
	if err != nil {
		t.Fatalf("first complete: %v", err)
	}
	if len(first.Content) != 1 || first.Content[0].Type != "tool_use" || first.Content[0].ID != "bash" {
		t.Fatalf("bad tool response: %+v", first.Content)
	}
	if len(first.Content[0].Raw) == 0 {
		t.Fatal("tool call did not preserve its Gemini part")
	}

	messages := []llm.Message{
		{Role: llm.RoleAssistant, Content: first.Content},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "tool_result", ToolUseID: first.Content[0].ID, Content: "/repo"}}},
	}
	if _, err := client.Complete(context.Background(), llm.Request{Model: "gemini-test", Messages: messages}); err != nil {
		t.Fatalf("continuation complete: %v", err)
	}

	call := requests[1].Contents[0].Parts[0]
	if call.ThoughtSignature != "opaque-signature" || call.FunctionCall == nil || call.FunctionCall.ID != "" {
		t.Fatalf("signature-bearing call was not replayed unchanged: %+v", call)
	}
	result := requests[1].Contents[1].Parts[0].FunctionResponse
	if result == nil || result.Name != "bash" || result.ID != "" || result.Response["output"] != "/repo" {
		t.Fatalf("function response did not retain the call's wire identity: %+v", result)
	}
}

func TestComplete_PreservesEmptySignaturePartWithoutShowingItAsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"visible"},{"text":"","thoughtSignature":"tail-signature"}]},"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "gemini-test"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(resp.Content) != 2 || resp.Content[0].Type != "text" || resp.Content[0].Text != "visible" || resp.Content[1].Type != "raw" {
		t.Fatalf("unexpected content: %+v", resp.Content)
	}
	parts := toContents([]llm.Message{{Role: llm.RoleAssistant, Content: resp.Content}})[0].Parts
	if len(parts) != 2 || parts[1].ThoughtSignature != "tail-signature" || parts[1].Text != "" {
		t.Fatalf("empty signature part was not replayed: %+v", parts)
	}
}

func TestComplete_DoesNotExposeThoughtParts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"internal summary","thought":true,"thoughtSignature":"thought-signature"},{"text":"answer"}]},"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "gemini-test"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(resp.Content) != 2 || resp.Content[0].Type != "raw" || resp.Content[1].Text != "answer" {
		t.Fatalf("unexpected content: %+v", resp.Content)
	}
}

func TestComplete_AssignsUniqueInternalIDsToParallelCallsWithoutWireIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read","args":{"path":"a"}}},{"functionCall":{"name":"read","args":{"path":"b"}}}]},"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "gemini-test"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(resp.Content) != 2 || resp.Content[0].ID != "read" || resp.Content[1].ID != "read_2" {
		t.Fatalf("parallel tool IDs are not unique: %+v", resp.Content)
	}
}

func TestComplete_PreservesAllParallelCallsAcrossContinuation(t *testing.T) {
	var requests []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var captured request
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests = append(requests, captured)
		if len(requests) == 1 {
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read","args":{"path":"a"}},"thoughtSignature":"parallel-signature"},{"functionCall":{"name":"read","args":{"path":"b"}}}]},"finishReason":"STOP"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()

	client := newTestClient(srv)
	first, err := client.Complete(context.Background(), llm.Request{Model: "gemini-test"})
	if err != nil {
		t.Fatalf("first complete: %v", err)
	}
	if len(first.Content) != 2 || first.Content[0].ID != "read" || first.Content[1].ID != "read_2" {
		t.Fatalf("unexpected internal calls: %+v", first.Content)
	}
	if len(first.Content[0].Raw) == 0 || len(first.Content[1].Raw) == 0 {
		t.Fatalf("not all parallel calls preserved: %+v", first.Content)
	}

	messages := []llm.Message{
		{Role: llm.RoleAssistant, Content: first.Content},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{
			{Type: "tool_result", ToolUseID: first.Content[0].ID, Content: "A"},
			{Type: "tool_result", ToolUseID: first.Content[1].ID, Content: "B"},
		}},
	}
	if _, err := client.Complete(context.Background(), llm.Request{Model: "gemini-test", Messages: messages}); err != nil {
		t.Fatalf("continuation complete: %v", err)
	}

	modelParts := requests[1].Contents[0].Parts
	if len(modelParts) != 2 || modelParts[0].FunctionCall.ID != "" || modelParts[1].FunctionCall.ID != "" {
		t.Fatalf("parallel calls gained synthetic wire IDs: %+v", modelParts)
	}
	if modelParts[0].ThoughtSignature != "parallel-signature" || modelParts[1].ThoughtSignature != "" {
		t.Fatalf("parallel signatures moved between parts: %+v", modelParts)
	}
	if modelParts[0].FunctionCall.Args["path"] != "a" || modelParts[1].FunctionCall.Args["path"] != "b" {
		t.Fatalf("parallel call order changed: %+v", modelParts)
	}
	resultParts := requests[1].Contents[1].Parts
	if len(resultParts) != 2 || resultParts[0].FunctionResponse.ID != "" || resultParts[1].FunctionResponse.ID != "" {
		t.Fatalf("parallel results gained synthetic wire IDs: %+v", resultParts)
	}
	if resultParts[0].FunctionResponse.Response["output"] != "A" || resultParts[1].FunctionResponse.Response["output"] != "B" {
		t.Fatalf("parallel result order changed: %+v", resultParts)
	}
}

func TestComplete_UsesErrorFieldForFailedToolResult(t *testing.T) {
	parts := toContents([]llm.Message{
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "tool_use", ID: "call_1", Name: "bash"}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "tool_result", ToolUseID: "call_1", Content: "denied", IsError: true}}},
	})[1].Parts
	response := parts[0].FunctionResponse.Response
	if response["error"] != "denied" || response["output"] != nil {
		t.Fatalf("bad failed function response: %+v", response)
	}
}

func TestComplete_ReturnsNonStopFinishReasonAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"SAFETY"}]}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "gemini-test"})
	if err == nil || !strings.Contains(err.Error(), "SAFETY") {
		t.Fatalf("expected SAFETY error, got %v", err)
	}
}

func TestComplete_ReturnsPromptBlockReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "gemini-test"})
	if err == nil || !strings.Contains(err.Error(), "PROHIBITED_CONTENT") {
		t.Fatalf("expected prompt block reason, got %v", err)
	}
}

func TestComplete_RetriesRequestTimeout(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "timeout", http.StatusRequestTimeout)
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()

	client := newTestClient(srv)
	client.MaxRetries = 1
	resp, err := client.Complete(context.Background(), llm.Request{Model: "gemini-test"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if attempts != 2 || len(resp.Content) != 1 || resp.Content[0].Text != "ok" {
		t.Fatalf("retry result: attempts=%d response=%+v", attempts, resp)
	}
}

func TestComplete_RejectsUnsupportedImageBeforeSending(t *testing.T) {
	sent := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = true
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Complete(context.Background(), llm.Request{Messages: []llm.Message{{
		Role: llm.RoleUser,
		Content: []llm.ContentBlock{{
			Type:   "image",
			Source: &llm.ImageSource{Type: "base64", MediaType: "image/gif", Data: "R0lGODlh"},
		}},
	}}})
	if err == nil || !strings.Contains(err.Error(), "image/gif") {
		t.Fatalf("expected unsupported image error, got %v", err)
	}
	if sent {
		t.Fatal("unsupported image reached Gemini API")
	}
}

func TestToParts_DropsForeignRawBlocks(t *testing.T) {
	parts := toContents([]llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		{Type: "raw", Raw: json.RawMessage(`{"type":"reasoning","encrypted_content":"secret"}`)},
		{Type: "text", Text: "answer"},
	}}})[0].Parts
	if len(parts) != 1 || parts[0].Text != "answer" {
		t.Fatalf("foreign raw block leaked into Gemini request: %+v", parts)
	}
}
