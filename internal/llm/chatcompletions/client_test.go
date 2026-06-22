package chatcompletions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
)

func TestBuildRequest_SystemMessagesToolsAndToolResults(t *testing.T) {
	req := llm.Request{
		Model:        "custom-model",
		SystemBlocks: []llm.SystemBlock{{Text: "system one"}, {Text: "system two"}},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hello"}}},
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "text", Text: "checking"}, {Type: "tool_use", ID: "call_1", Name: "read", Input: map[string]any{"path": "README.md"}}}},
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "tool_result", ToolUseID: "call_1", Content: "contents"}}},
		},
		Tools: []llm.ToolSpec{{Name: "read", Description: "read a file", InputSchema: map[string]any{"type": "object"}}},
	}

	got := BuildRequest(req, req.Model)
	if got.Model != "custom-model" {
		t.Fatalf("model = %q", got.Model)
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "system one\n\nsystem two" {
		t.Fatalf("system message = %#v", got.Messages[0])
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "hello" {
		t.Fatalf("user message = %#v", got.Messages[1])
	}
	assistant := got.Messages[2]
	if assistant.Role != "assistant" || assistant.Content != "checking" || len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant message = %#v", assistant)
	}
	if assistant.ToolCalls[0].ID != "call_1" || assistant.ToolCalls[0].Function.Name != "read" {
		t.Fatalf("tool call = %#v", assistant.ToolCalls[0])
	}
	if got.Messages[3].Role != "tool" || got.Messages[3].ToolCallID != "call_1" || got.Messages[3].Content != "contents" {
		t.Fatalf("tool result = %#v", got.Messages[3])
	}
	if got.ToolChoice != "auto" || len(got.Tools) != 1 || got.Tools[0].Function.Name != "read" {
		t.Fatalf("tools = %#v choice=%q", got.Tools, got.ToolChoice)
	}
}

func TestBuildRequest_ImageContentUsesChatCompletionPartsInOrder(t *testing.T) {
	got := BuildRequest(llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{
		{Type: "image", Source: &llm.ImageSource{MediaType: "image/png", Data: "aW1hZ2U="}},
		{Type: "text", Text: "what is shown?"},
	}}}}, "model")

	parts, ok := got.Messages[0].Content.([]ContentPart)
	if !ok {
		t.Fatalf("content type = %T, want []ContentPart", got.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %#v", parts)
	}
	if parts[0].Type != "image_url" || parts[0].ImageURL == nil || parts[0].ImageURL.URL != "data:image/png;base64,aW1hZ2U=" {
		t.Fatalf("image part = %#v", parts[0])
	}
	if parts[1].Type != "text" || parts[1].Text != "what is shown?" {
		t.Fatalf("text part = %#v", parts[1])
	}
}

func TestToLLMResponse_TextUsageAndToolCalls(t *testing.T) {
	resp, err := ToLLMResponse(Response{
		Choices: []Choice{{
			FinishReason: "tool_calls",
			Message:      Message{Content: "use tool", ToolCalls: []ToolCall{{ID: "call_2", Type: "function", Function: FunctionCall{Name: "bash", Arguments: `{"command":"go test"}`}}}},
		}},
		Usage: &Usage{PromptTokens: 11, CompletionTokens: 7},
	})
	if err != nil {
		t.Fatalf("ToLLMResponse: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Fatalf("stop = %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %#v", resp.Usage)
	}
	if len(resp.Content) != 2 || resp.Content[0].Text != "use tool" || resp.Content[1].Name != "bash" {
		t.Fatalf("content = %#v", resp.Content)
	}
	if resp.Content[1].Input["command"] != "go test" {
		t.Fatalf("tool input = %#v", resp.Content[1].Input)
	}
}

func TestClientComplete_SurfaceProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"tools are not supported by this model"}}`, http.StatusBadRequest)
	}))
	defer server.Close()

	client := &Client{ProviderName: "openrouter", APIKey: "secret", Endpoint: server.URL, DefaultModel: "default", HTTP: server.Client(), MaxRetries: -1}
	_, err := client.Complete(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}}})
	if err == nil {
		t.Fatal("expected provider error")
	}
	for _, want := range []string{"openrouter", "400", "tools are not supported"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestClientComplete_PostsChatCompletionRequest(t *testing.T) {
	var gotAuth string
	var got Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
	}))
	defer server.Close()

	client := &Client{ProviderName: "test-provider", APIKey: "secret", Endpoint: server.URL, DefaultModel: "default", HTTP: server.Client(), MaxRetries: -1}
	resp, err := client.Complete(context.Background(), llm.Request{System: "sys", Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if got.Model != "default" || len(got.Messages) != 2 || got.Messages[1].Content != "hi" {
		t.Fatalf("request = %#v", got)
	}
	if resp.Content[0].Text != "ok" || resp.StopReason != "end_turn" {
		t.Fatalf("response = %#v", resp)
	}
}
