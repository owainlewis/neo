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
		w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":3,"output_tokens":1}}`))
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
		w.Write([]byte(`{"status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"bash","arguments":"{\"cmd\":\"ls\"}"}]}`))
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

	// Request translation: system -> instructions, one user message input item,
	// and one flat function tool.
	if captured.Instructions != "you are neo" {
		t.Fatalf("instructions: got %q", captured.Instructions)
	}
	if len(captured.Input) != 1 || captured.Input[0].Type != "message" || captured.Input[0].Role != "user" {
		t.Fatalf("bad outgoing input: %+v", captured.Input)
	}
	if len(captured.Input[0].Content) != 1 || captured.Input[0].Content[0].Type != "input_text" {
		t.Fatalf("bad user content part: %+v", captured.Input[0].Content)
	}
	if len(captured.Tools) != 1 || captured.Tools[0].Name != "bash" || captured.Tools[0].Type != "function" {
		t.Fatalf("bad outgoing tools: %+v", captured.Tools)
	}
	if captured.Store {
		t.Fatalf("store must be false")
	}

	// Response translation: function_call -> tool_use, keyed by call_id.
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

func TestToInput_ReplaysRawReasoningBeforeToolResult(t *testing.T) {
	reasoning := json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"secret"}`)
	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: "raw", Raw: reasoning},
				{Type: "tool_use", ID: "call_1", Name: "bash", Input: map[string]any{"cmd": "ls"}},
			}},
			{Role: llm.RoleUser, Content: []llm.ContentBlock{
				{Type: "tool_result", ToolUseID: "call_1", Content: "file.txt"},
			}},
		},
	}

	body, err := json.Marshal(apiRequest{Model: "m", Input: toInput(req), Store: false})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var got struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if len(got.Input) != 3 {
		t.Fatalf("expected 3 input items, got %d: %s", len(got.Input), body)
	}
	if string(got.Input[0]) != string(reasoning) {
		t.Fatalf("reasoning item was not replayed verbatim:\n got %s\nwant %s", got.Input[0], reasoning)
	}
}

func TestToInput_PreservesAssistantItemOrder(t *testing.T) {
	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: "text", Text: "I will check."},
				{Type: "tool_use", ID: "call_1", Name: "bash", Input: map[string]any{"cmd": "ls"}},
				{Type: "text", Text: "Done."},
			}},
			{Role: llm.RoleUser, Content: []llm.ContentBlock{
				{Type: "tool_result", ToolUseID: "call_1", Content: "file.txt"},
			}},
		},
	}

	items := toInput(req)
	if len(items) != 4 {
		t.Fatalf("expected 4 input items, got %d: %+v", len(items), items)
	}
	if items[0].Type != "message" || items[0].Content[0].Text != "I will check." {
		t.Fatalf("assistant preamble not first: %+v", items[0])
	}
	if items[1].Type != "function_call" || items[1].CallID != "call_1" {
		t.Fatalf("tool call not second: %+v", items[1])
	}
	if items[2].Type != "message" || items[2].Content[0].Text != "Done." {
		t.Fatalf("assistant follow-up not third: %+v", items[2])
	}
	if items[3].Type != "function_call_output" || items[3].CallID != "call_1" {
		t.Fatalf("tool result not fourth: %+v", items[3])
	}
}

func TestToResponse_PreservesReasoningItems(t *testing.T) {
	raw := []byte(`{"status":"completed","output":[{"type":"reasoning","id":"rs_1","encrypted_content":"secret"},{"type":"function_call","call_id":"call_1","name":"bash","arguments":"{\"cmd\":\"ls\"}"}]}`)
	var out apiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	resp, err := toResponse(out)
	if err != nil {
		t.Fatalf("toResponse: %v", err)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("expected reasoning + tool use, got %+v", resp.Content)
	}
	if resp.Content[0].Type != "raw" || string(resp.Content[0].Raw) != `{"type":"reasoning","id":"rs_1","encrypted_content":"secret"}` {
		t.Fatalf("reasoning not preserved: %+v", resp.Content[0])
	}
	if resp.Content[1].Type != "tool_use" || resp.Content[1].ID != "call_1" {
		t.Fatalf("tool call not preserved: %+v", resp.Content[1])
	}
}

func TestToInput_ToolUseAndResult(t *testing.T) {
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
	items := toInput(req)
	if len(items) != 2 {
		t.Fatalf("expected 2 input items, got %d: %+v", len(items), items)
	}
	if items[0].Type != "function_call" || items[0].CallID != "call_1" || items[0].Name != "bash" {
		t.Fatalf("bad function_call item: %+v", items[0])
	}
	if items[0].Arguments != `{"cmd":"ls"}` {
		t.Fatalf("bad arguments: %q", items[0].Arguments)
	}
	if items[1].Type != "function_call_output" || items[1].CallID != "call_1" || items[1].Output != "file.txt" {
		t.Fatalf("bad function_call_output item: %+v", items[1])
	}
}

// TestToInput_EmptyToolOutputStillSerialized guards against the Responses API
// 400 "Missing required parameter: 'input[N].output'": a tool that produces no
// output must still emit an "output" field on its function_call_output item.
func TestToInput_EmptyToolOutputStillSerialized(t *testing.T) {
	req := llm.Request{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{
			{Type: "tool_result", ToolUseID: "call_1", Content: ""},
		}},
	}}

	items := toInput(req)
	if len(items) != 1 || items[0].Type != "function_call_output" {
		t.Fatalf("want 1 function_call_output, got %+v", items)
	}
	b, err := json.Marshal(items[0])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["output"]; !ok {
		t.Fatalf("function_call_output must include 'output' even when empty; got %s", b)
	}
}

func TestToInput_ImageBecomesInputImage(t *testing.T) {
	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{
				{Type: "image", Source: &llm.ImageSource{Type: "base64", MediaType: "image/png", Data: "QUJD"}},
				{Type: "text", Text: "what is this"},
			}},
		},
	}
	items := toInput(req)
	if len(items) != 1 || items[0].Type != "message" || len(items[0].Content) != 2 {
		t.Fatalf("bad image input: %+v", items)
	}
	if items[0].Content[0].Type != "input_image" || items[0].Content[0].ImageURL != "data:image/png;base64,QUJD" {
		t.Fatalf("bad image part: %+v", items[0].Content[0])
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

func TestStopReason_MaxTokensAndCachedUsage(t *testing.T) {
	out := apiResponse{
		Status:            "incomplete",
		IncompleteDetails: &incomplete{Reason: "max_output_tokens"},
		Usage:             &responseUsage{InputTokens: 10, OutputTokens: 2},
	}
	out.Usage.InputTokensDetails = &struct {
		CachedTokens int `json:"cached_tokens"`
	}{CachedTokens: 7}

	resp, err := toResponse(out)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.StopReason != "max_tokens" {
		t.Fatalf("stop reason: got %q want max_tokens", resp.StopReason)
	}
	if resp.Usage.CacheReadTokens != 7 {
		t.Fatalf("cached tokens: got %d want 7", resp.Usage.CacheReadTokens)
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
		w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "ok" {
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
