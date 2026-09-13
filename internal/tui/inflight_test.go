package tui

import (
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/agent"
)

func groupedCall(kind agent.EventKind, id, name string, args map[string]any, text string, failed bool) agent.Event {
	return agent.Event{Kind: kind, GroupID: "g1", GroupSize: 2, ToolUseID: id, Name: name, Args: args, Text: text, IsError: failed}
}

// Parallel calls are ordinary receipts: the status line counts them while
// they run, and each result appends its own line as it lands.
func TestParallelCallsRenderAsReceiptsInCompletionOrder(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	a := map[string]any{"path": "a.go"}
	b := map[string]any{"path": "b.go"}
	m.handleEvent(agent.Event{Kind: agent.EventParallelStart, GroupID: "g1", GroupSize: 2, Calls: []agent.ToolCallRef{
		{ID: "a", Name: "read_file", Args: a}, {ID: "b", Name: "read_file", Args: b},
	}})
	m.handleEvent(groupedCall(agent.EventToolCall, "a", "read_file", a, "", false))
	m.handleEvent(groupedCall(agent.EventToolCall, "b", "read_file", b, "", false))

	if len(m.blocks) != 0 {
		t.Fatalf("starting calls appended %d blocks, want none until results arrive", len(m.blocks))
	}
	if got := plain(m.statusLine()); !strings.Contains(got, "2 tools in parallel") {
		t.Fatalf("status = %q, want parallel count", got)
	}

	m.handleEvent(groupedCall(agent.EventToolResult, "b", "read_file", b, "package b", false))
	if got := plain(m.statusLine()); !strings.Contains(got, "Reading a.go") {
		t.Fatalf("status after one result = %q, want the remaining call", got)
	}
	m.handleEvent(groupedCall(agent.EventToolResult, "a", "read_file", a, "package a", false))

	if len(m.blocks) != 2 {
		t.Fatalf("blocks = %d, want one receipt per result", len(m.blocks))
	}
	first := renderPlain(m.blocks[0], 80)
	second := renderPlain(m.blocks[1], 80)
	if !strings.Contains(first, "Read b.go") || !strings.Contains(second, "Read a.go") {
		t.Fatalf("receipts = %q, %q; want completion order", first, second)
	}
	if len(m.inflight) != 0 {
		t.Fatalf("inflight = %#v, want empty after all results", m.inflight)
	}
}

func TestParallelSubagentsAreCountedAsSubagents(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	for _, id := range []string{"a", "b"} {
		m.handleEvent(groupedCall(agent.EventToolCall, id, "agent", map[string]any{"prompt": "inspect " + id}, "", false))
	}
	if got := plain(m.statusLine()); !strings.Contains(got, "2 subagents in parallel") {
		t.Fatalf("status = %q", got)
	}
}

// An agent call gets one receipt on success and an error card when the child
// reports ok=false inside an otherwise successful tool result.
func TestAgentCallReceiptAndFailureCard(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	args := map[string]any{"prompt": "find the flaky test"}
	m.handleEvent(agent.Event{Kind: agent.EventToolCall, ToolUseID: "x", Name: "agent", Args: args})
	if got := plain(m.statusLine()); !strings.Contains(got, "find the flaky test") {
		t.Fatalf("status = %q, want the delegated prompt", got)
	}
	m.handleEvent(agent.Event{Kind: agent.EventToolResult, ToolUseID: "x", Name: "agent", Args: args, Text: `{"ok":true,"kind":"agent","took":"3s","code":""}` + "\nreport"})
	if len(m.blocks) != 1 || !strings.Contains(renderPlain(m.blocks[0], 80), "Delegated find the flaky test") {
		t.Fatalf("success blocks = %#v", m.blocks)
	}

	m.handleEvent(agent.Event{Kind: agent.EventToolCall, ToolUseID: "y", Name: "agent", Args: args})
	m.handleEvent(agent.Event{Kind: agent.EventToolResult, ToolUseID: "y", Name: "agent", Args: args, Text: `{"ok":false,"kind":"agent","took":"3s","code":"timeout"}` + "\nsubagent hit its wall-clock limit"})
	last, ok := m.blocks[len(m.blocks)-1].(toolResultBlock)
	if !ok || !last.isError || !strings.Contains(last.text, "wall-clock") {
		t.Fatalf("failed agent call should render an error card, got %#v", m.blocks[len(m.blocks)-1])
	}
}

// A turn that ends with calls still tracked (cancellation, provider error)
// must not leave the status line describing them.
func TestSendResultClearsInflightCalls(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.handleEvent(agent.Event{Kind: agent.EventToolCall, ToolUseID: "a", Name: "bash", Args: map[string]any{"command": "sleep 100"}})
	m.Update(sendResultMsg{err: nil})
	if len(m.inflight) != 0 {
		t.Fatalf("inflight = %#v after the turn ended", m.inflight)
	}
}
