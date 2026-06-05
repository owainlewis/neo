package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/tools"
)

func newTestAgent(t *testing.T, prov llm.Provider, ts ...tools.Tool) *Agent {
	t.Helper()
	return New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(ts...),
	})
}

func TestAgent_TextOnlyTurnReturnsText(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("hello world")}}
	ag := newTestAgent(t, prov)

	out, err := ag.Send(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello world" {
		t.Fatalf("expected 'hello world', got %q", out)
	}
	// One user message, one assistant message.
	if got := len(ag.Transcript()); got != 2 {
		t.Fatalf("expected 2 messages in transcript, got %d", got)
	}
}

// echoTool returns whatever was passed in the "text" field. Used to drive
// tool_use → tool_result cycles in tests.
type echoTool struct{}

func (echoTool) Name() string { return "echo" }
func (echoTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "echo", Description: "echo", InputSchema: map[string]any{"type": "object"}}
}
func (echoTool) Run(_ context.Context, in map[string]any) (string, error) {
	if s, ok := in["text"].(string); ok {
		return s, nil
	}
	return "", nil
}

func TestAgent_ToolUseFollowedByText(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "echo", map[string]any{"text": "pong"}),
		llmtest.Text("done"),
	}}
	ag := newTestAgent(t, prov, echoTool{})

	out, err := ag.Send(context.Background(), "ping")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "done" {
		t.Fatalf("expected final text 'done', got %q", out)
	}

	// Transcript invariant: every assistant tool_use must be followed by a
	// user tool_result with a matching ToolUseID.
	assertToolUseResultsPaired(t, ag.Transcript())
}

func TestAgent_RestoresTranscriptFromConfig(t *testing.T) {
	prior := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "first"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "text", Text: "first reply"}}},
	}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("second reply")}}
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(),
		Messages: prior,
	})

	out, err := ag.Send(context.Background(), "second")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "second reply" {
		t.Fatalf("unexpected output: %q", out)
	}
	if got := len(prov.Calls[0].Messages); got != 3 {
		t.Fatalf("provider saw %d messages, want 3", got)
	}
	if got := prov.Calls[0].Messages[0].Content[0].Text; got != "first" {
		t.Fatalf("provider did not receive restored first message: %q", got)
	}
}

func TestAgent_TranscriptReturnsCopy(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("hello")}}
	ag := newTestAgent(t, prov)
	if _, err := ag.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	transcript := ag.Transcript()
	transcript[0].Content[0].Text = "mutated"
	if got := ag.Transcript()[0].Content[0].Text; got == "mutated" {
		t.Fatal("Transcript allowed caller to mutate agent state")
	}
}

func TestAgent_ProviderErrorLeavesTranscriptClean(t *testing.T) {
	// First call returns OK with a tool_use; second call (after we feed the
	// tool result back) errors. The transcript must still satisfy the
	// tool_use/tool_result pairing invariant so a follow-up Send doesn't 400.
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "echo", map[string]any{"text": "x"}),
		// no second response → fake returns an error
	}}
	ag := newTestAgent(t, prov, echoTool{})

	_, err := ag.Send(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error from provider")
	}
	assertToolUseResultsPaired(t, ag.Transcript())
}

func TestAgent_MaxTurnsReturnsSentinelWithPartialText(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		textAndToolUse("first partial", "call_1"),
		textAndToolUse("second partial", "call_2"),
	}}
	var events []Event
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(echoTool{}),
		MaxTurns: 2,
		OnEvent: func(e Event) {
			events = append(events, e)
		},
	})

	out, err := ag.Send(context.Background(), "hi")
	if !errors.Is(err, ErrMaxTurns) {
		t.Fatalf("expected ErrMaxTurns, got %v", err)
	}
	if out != "first partial\nsecond partial" {
		t.Fatalf("expected accumulated partial text, got %q", out)
	}
	if got := len(prov.Calls); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}
	if !sawMaxTurnsEvent(events, 2) {
		t.Fatalf("missing max-turns event in %#v", events)
	}
	assertToolUseResultsPaired(t, ag.Transcript())
}

func TestAgent_ApprovalAllowsTool(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "echo", map[string]any{"text": "pong"}),
		llmtest.Text("done"),
	}}
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(echoTool{}),
		Policy:   permission.New("ask", "."),
		Approve: func(context.Context, ApprovalRequest) (bool, error) {
			return true, nil
		},
	})
	out, err := ag.Send(context.Background(), "ping")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out != "done" {
		t.Fatalf("out = %q", out)
	}
	assertToolUseResultsPaired(t, ag.Transcript())
}

func TestAgent_ApprovalDenialBecomesToolError(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "echo", map[string]any{"text": "pong"}),
		llmtest.Text("done"),
	}}
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(echoTool{}),
		Policy:   permission.New("ask", "."),
		Approve: func(context.Context, ApprovalRequest) (bool, error) {
			return false, nil
		},
	})
	if _, err := ag.Send(context.Background(), "ping"); err != nil {
		t.Fatalf("send: %v", err)
	}
	msgs := ag.Transcript()
	result := msgs[2].Content[0]
	if !result.IsError || !strings.Contains(result.Content, "denied") {
		t.Fatalf("tool result = %+v, want denial error", result)
	}
}

func TestAgent_MissingApproverDeniesAskedTool(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "echo", map[string]any{"text": "pong"}),
		llmtest.Text("done"),
	}}
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(echoTool{}),
		Policy:   permission.New("ask", "."),
	})
	if _, err := ag.Send(context.Background(), "ping"); err != nil {
		t.Fatalf("send: %v", err)
	}
	msgs := ag.Transcript()
	result := msgs[2].Content[0]
	if !result.IsError || !strings.Contains(result.Content, "no approver") {
		t.Fatalf("tool result = %+v, want missing approver error", result)
	}
}

func TestAgent_AccumulatesUsage(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{{Type: "text", Text: "one"}}, StopReason: "end_turn", Usage: llm.Usage{InputTokens: 1, OutputTokens: 2, CacheCreationTokens: 3, CacheReadTokens: 4}},
	}}
	ag := newTestAgent(t, prov)
	if _, err := ag.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("send: %v", err)
	}
	want := llm.Usage{InputTokens: 1, OutputTokens: 2, CacheCreationTokens: 3, CacheReadTokens: 4}
	if got := ag.Usage(); got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
}

func TestAgent_CancelledProviderReturnsContextCanceled(t *testing.T) {
	ag := newTestAgent(t, cancelProvider{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ag.Send(ctx, "hi")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestAgent_CompactorRunsBeforeProvider(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok")}}
	comp := &countingCompactor{}
	ag := New(Config{
		Model:     "test-model",
		Provider:  prov,
		Tools:     tools.NewRegistry(),
		Compactor: comp,
	})
	if _, err := ag.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if comp.calls != 1 {
		t.Fatalf("compactor calls = %d, want 1", comp.calls)
	}
}

func textAndToolUse(text, id string) llm.Response {
	return llm.Response{
		Content: []llm.ContentBlock{
			{Type: "text", Text: text},
			{Type: "tool_use", ID: id, Name: "echo", Input: map[string]any{"text": "again"}},
		},
		StopReason: "tool_use",
	}
}

func sawMaxTurnsEvent(events []Event, limit int) bool {
	for _, event := range events {
		if event.Kind == EventMaxTurnsReached && event.MaxTurns == limit && errors.Is(event.Err, ErrMaxTurns) {
			return true
		}
	}
	return false
}

func assertToolUseResultsPaired(t *testing.T, msgs []llm.Message) {
	t.Helper()
	pendingIDs := map[string]bool{}
	for _, m := range msgs {
		for _, b := range m.Content {
			switch b.Type {
			case "tool_use":
				pendingIDs[b.ID] = true
			case "tool_result":
				delete(pendingIDs, b.ToolUseID)
			}
		}
	}
	if len(pendingIDs) > 0 {
		ids := make([]string, 0, len(pendingIDs))
		for id := range pendingIDs {
			ids = append(ids, id)
		}
		t.Fatalf("transcript has unmatched tool_use IDs: %s", strings.Join(ids, ","))
	}
}

type cancelProvider struct{}

func (cancelProvider) Name() string { return "cancel" }
func (cancelProvider) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return nil, ctx.Err()
}

type countingCompactor struct {
	compact.NoCompaction
	calls int
}

func (c *countingCompactor) Compact(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
	c.calls++
	if len(messages) == 0 {
		return nil, fmt.Errorf("expected user message before compaction")
	}
	return messages, nil
}
