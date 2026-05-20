package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
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
