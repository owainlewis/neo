package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/factory"
	"github.com/owainlewis/neo/internal/workflow"
)

func parallelStart(group string, calls ...agent.ToolCallRef) agent.Event {
	return agent.Event{Kind: agent.EventParallelStart, GroupID: group, GroupSize: len(calls), Calls: calls}
}

func parallelToolEvent(kind agent.EventKind, group, id, name string, pos int, text string, failed bool) agent.Event {
	return agent.Event{Kind: kind, GroupID: group, GroupSize: 2, GroupPos: pos,
		ToolUseID: id, Name: name, Text: text, IsError: failed}
}

func TestParallelToolsKeepSourceOrderAndStableHeight(t *testing.T) {
	m := makeTestModel()
	m.handleEvent(parallelStart("g1",
		agent.ToolCallRef{ID: "a", Name: "read_file", Args: map[string]any{"path": "a.go"}},
		agent.ToolCallRef{ID: "b", Name: "read_file", Args: map[string]any{"path": "b.go"}},
	))
	group, ok := m.blocks[0].(*parallelBlock)
	if !ok || len(group.rows) != 2 {
		t.Fatalf("parallel block = %#v", m.blocks)
	}
	wantLines := len(strings.Split(renderPlain(group, 80), "\n"))
	if wantLines != 3 {
		t.Fatalf("lines = %d, want header + two rows", wantLines)
	}
	// A reused call ID from another group must not update this row.
	m.handleEvent(parallelToolEvent(agent.EventToolResult, "other", "a", "read_file", 0, "wrong", false))
	if group.rows[0].state != parallelRunning {
		t.Fatal("mismatched group updated a row")
	}
	// Duplicate group starts are idempotent.
	m.handleEvent(parallelStart("g1",
		agent.ToolCallRef{ID: "a", Name: "read_file"},
		agent.ToolCallRef{ID: "b", Name: "read_file"},
	))
	if len(m.blocks) != 1 {
		t.Fatalf("duplicate start added a block: %d", len(m.blocks))
	}

	// Finish in reverse order. Identity, not name or arrival order, chooses rows.
	m.handleEvent(parallelToolEvent(agent.EventToolResult, "g1", "b", "read_file", 1, "b", false))
	if group.rows[0].state != parallelRunning || group.rows[1].state != parallelSucceeded {
		t.Fatalf("partial states = %v, %v", group.rows[0].state, group.rows[1].state)
	}
	m.handleEvent(parallelToolEvent(agent.EventToolResult, "g1", "a", "read_file", 0, "a", false))
	if group.rows[0].state != parallelSucceeded || group.rows[1].state != parallelSucceeded {
		t.Fatalf("complete states = %v, %v", group.rows[0].state, group.rows[1].state)
	}
	if got := len(strings.Split(renderPlain(group, 80), "\n")); got != wantLines {
		t.Fatalf("completed height = %d, running height = %d", got, wantLines)
	}
	out := renderPlain(group, 80)
	if strings.Index(out, "a.go") > strings.Index(out, "b.go") {
		t.Fatalf("rows reordered by completion:\n%s", out)
	}
	for _, width := range []int{40, 80, 120} {
		for i, line := range strings.Split(group.render(width, nil), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d line %d is %d cells: %q", width, i, got, plain(line))
			}
		}
	}
}

func TestParallelSubagentEventsUpdatePreallocatedRows(t *testing.T) {
	m := makeTestModel()
	m.handleEvent(parallelStart("agents",
		agent.ToolCallRef{ID: "a", Name: "agent", Args: map[string]any{"prompt": "Inspect auth"}},
		agent.ToolCallRef{ID: "b", Name: "agent", Args: map[string]any{"prompt": "Find tests"}},
	))
	// Parallel metadata that cannot be correlated is consumed, never rendered
	// as a duplicate legacy tree.
	m.handleStepEvent(factory.Event{Node: 99, CallID: "a", GroupID: "wrong",
		Ev: factory.AgentEvent{Kind: "start"}})
	if len(m.blocks) != 1 {
		t.Fatalf("mismatched parallel event created a tree: %#v", m.blocks)
	}
	m.handleStepEvent(factory.Event{Node: 7, Task: "Inspect auth", CallID: "a", GroupID: "agents", GroupSize: 2, GroupPos: 0,
		Ev: factory.AgentEvent{Kind: "start"}})
	m.handleStepEvent(factory.Event{Node: 7, CallID: "a", GroupID: "agents", GroupSize: 2, GroupPos: 0,
		Ev: factory.AgentEvent{Kind: "tool", Body: "read auth.go"}})

	if len(m.blocks) != 1 {
		t.Fatalf("supervisor created a duplicate block: %#v", m.blocks)
	}
	group := m.blocks[0].(*parallelBlock)
	if group.rows[0].detail != "read auth.go" {
		t.Fatalf("live detail = %q", group.rows[0].detail)
	}
	m.handleStepEvent(factory.Event{Node: 7, CallID: "a", GroupID: "agents", Ev: factory.AgentEvent{Kind: "done"}})
	if group.rows[0].state != parallelSucceeded || group.rows[1].state != parallelRunning {
		t.Fatalf("states = %v, %v", group.rows[0].state, group.rows[1].state)
	}
	if out := renderPlain(group, 80); !strings.Contains(out, "2 subagents in parallel") || !strings.Contains(out, "Find tests") {
		t.Fatalf("subagent group:\n%s", out)
	}
}

func TestParallelSubagentRetryCanRecoverFromFailure(t *testing.T) {
	m := makeTestModel()
	m.handleEvent(parallelStart("agents",
		agent.ToolCallRef{ID: "a", Name: "agent", Args: map[string]any{"prompt": "Inspect auth"}},
		agent.ToolCallRef{ID: "b", Name: "agent", Args: map[string]any{"prompt": "Find tests"}},
	))
	event := func(node int, kind string) factory.Event {
		return factory.Event{Node: node, CallID: "a", GroupID: "agents", Ev: factory.AgentEvent{Kind: kind}}
	}
	m.handleStepEvent(event(7, "start"))
	m.handleStepEvent(event(7, "fail"))
	m.handleStepEvent(event(8, "start"))
	row := m.parallelCalls["a"]
	if row.state != parallelRunning || row.nodeID != 8 {
		t.Fatalf("retry did not restart row: %+v", row)
	}
	// A late event from the first attempt cannot overwrite the retry.
	m.handleStepEvent(event(7, "fail"))
	if row.state != parallelRunning {
		t.Fatalf("late attempt settled retry: %+v", row)
	}
	m.handleStepEvent(event(8, "done"))
	m.handleEvent(parallelToolEvent(agent.EventToolResult, "agents", "a", "agent", 0,
		`{"ok":true,"kind":"agent","took":"1s"}`+"\nreport", false))
	if row.state != parallelSucceeded {
		t.Fatalf("authoritative success did not settle retry: %+v", row)
	}
}

func TestParallelSubagentIgnoresSupervisorEventsAfterParentSettlement(t *testing.T) {
	m := makeTestModel()
	m.handleEvent(parallelStart("agents",
		agent.ToolCallRef{ID: "a", Name: "agent", Args: map[string]any{"prompt": "Inspect auth"}},
		agent.ToolCallRef{ID: "b", Name: "agent", Args: map[string]any{"prompt": "Find tests"}},
	))
	m.handleEvent(parallelToolEvent(agent.EventToolResult, "agents", "a", "agent", 0,
		`{"ok":false,"kind":"agent","code":"execution_error"}`+"\nfailed", true))
	row := m.parallelCalls["a"]
	if row.state != parallelFailed || !row.parentSettled {
		t.Fatalf("parent result did not settle row: %+v", row)
	}

	for _, kind := range []string{"start", "tool", "done"} {
		m.handleStepEvent(factory.Event{Node: 9, CallID: "a", GroupID: "agents",
			Ev: factory.AgentEvent{Kind: kind, Body: "late detail"}})
	}
	if row.state != parallelFailed || row.nodeID != 0 || row.detail != "" {
		t.Fatalf("late supervisor event rewrote parent result: %+v", row)
	}
}

func TestParallelPlainTextSnapshots(t *testing.T) {
	group := &parallelBlock{id: "g", kind: "tools", rows: []*parallelCallRow{
		{id: "a", name: "read_file", args: map[string]any{"path": "a.go"}, elapsed: time.Second, state: parallelSucceeded},
		{id: "b", name: "grep", args: map[string]any{"pattern": "Event"}, elapsed: 500 * time.Millisecond, state: parallelSucceeded},
	}}
	want := "✓ 2 tools in parallel  1s\n├─ ✓ Read a.go  1s\n└─ ✓ Searched Event  <1s"
	for _, width := range []int{40, 80, 120} {
		if got := renderPlain(group, width); got != want {
			t.Fatalf("width %d snapshot:\n%s\nwant:\n%s", width, got, want)
		}
	}
}

func TestParallelRowsPreserveElapsedAtNarrowWidth(t *testing.T) {
	started := time.Now().Add(-2 * time.Second)
	tests := []struct {
		name string
		row  *parallelCallRow
		want string
	}{
		{
			name: "tool",
			row: &parallelCallRow{name: "read_file", startAt: started, detail: "Reading a very long repository path",
				args: map[string]any{"path": "internal/a/very/long/path/to/parallel_execution_runtime.go"}},
			want: "Reading",
		},
		{
			name: "subagent",
			row: &parallelCallRow{name: "agent", startAt: started, detail: "Searching every permissions implementation",
				args: map[string]any{"prompt": "Inspect the complete authorization and permission boundary implementation"}},
			want: "Inspect the",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group := &parallelBlock{kind: "tools", startAt: started, rows: []*parallelCallRow{
				tt.row,
				{name: "glob", startAt: started, args: map[string]any{"pattern": "*.go"}},
			}}
			lines := strings.Split(renderPlain(group, 40), "\n")
			if len(lines) != 3 {
				t.Fatalf("rendered lines=%q", lines)
			}
			if !strings.Contains(lines[1], tt.want) || !strings.Contains(lines[1], "2s") {
				t.Fatalf("primary label or elapsed lost at 40 columns: %q", lines[1])
			}
			if strings.Contains(lines[1], "Reading a very") || strings.Contains(lines[1], "Searching every") {
				t.Fatalf("detail should be dropped before label or elapsed: %q", lines[1])
			}
			if got := lipgloss.Width(lines[1]); got > 40 {
				t.Fatalf("row width=%d: %q", got, lines[1])
			}
		})
	}
}

func TestParallelElapsedRefreshesOnSpinnerTick(t *testing.T) {
	m := makeTestModel()
	m.handleEvent(parallelStart("g1",
		agent.ToolCallRef{ID: "a", Name: "read_file", Args: map[string]any{"path": "a.go"}},
		agent.ToolCallRef{ID: "b", Name: "glob", Args: map[string]any{"pattern": "*.go"}},
	))
	group := m.parallelGroups["g1"]
	group.startAt = time.Now().Add(-2 * time.Second)
	for _, row := range group.rows {
		row.startAt = group.startAt
	}
	m.Update(spinner.TickMsg{})
	if got := plain(m.viewport.View()); !strings.Contains(got, "2s") {
		t.Fatalf("parallel elapsed did not repaint on tick:\n%s", got)
	}
}

func TestParallelFailureAndCancellationSettleRows(t *testing.T) {
	m := makeTestModel()
	m.handleEvent(parallelStart("g1",
		agent.ToolCallRef{ID: "a", Name: "grep", Args: map[string]any{"pattern": "x"}},
		agent.ToolCallRef{ID: "b", Name: "glob", Args: map[string]any{"pattern": "*.go"}},
	))
	m.handleEvent(parallelToolEvent(agent.EventToolResult, "g1", "a", "grep", 0, "permission denied", true))
	m.handleEvent(parallelToolEvent(agent.EventToolResult, "g1", "b", "glob", 1, "skipped because the active turn was canceled", true))
	group := m.parallelGroups["g1"]
	if group.running() || group.rows[0].state != parallelFailed || group.rows[1].state != parallelCancelled {
		t.Fatalf("settled states = %v, %v", group.rows[0].state, group.rows[1].state)
	}
	if got := len(m.blocks); got != 2 {
		t.Fatalf("blocks = %d, want group + error", got)
	}
	if out := renderPlain(group, 80); !strings.Contains(out, "✗ 2 tools in parallel") || !strings.Contains(out, "└─ -") {
		t.Fatalf("failure render:\n%s", out)
	}
}

func TestDuplicateParentResultCannotRewriteTerminalRow(t *testing.T) {
	m := makeTestModel()
	m.handleEvent(parallelStart("g1",
		agent.ToolCallRef{ID: "a", Name: "read_file", Args: map[string]any{"path": "a.go"}},
		agent.ToolCallRef{ID: "b", Name: "glob", Args: map[string]any{"pattern": "*.go"}},
	))
	m.handleEvent(parallelToolEvent(agent.EventToolResult, "g1", "a", "read_file", 0, "ok", false))
	m.handleEvent(parallelToolEvent(agent.EventToolResult, "g1", "a", "read_file", 0, "canceled", true))
	row := m.parallelCalls["a"]
	if row.state != parallelSucceeded || m.turn.errors != 0 || len(m.blocks) != 1 {
		t.Fatalf("duplicate result rewrote row: state=%v errors=%d blocks=%d", row.state, m.turn.errors, len(m.blocks))
	}
}

func TestParallelStatusKeepsWorkflowFirst(t *testing.T) {
	m := makeTestModel()
	m.width = 140
	m.busy = true
	m.busySince = time.Now().Add(-3 * time.Second)
	m.workflow = &workflowBlock{items: []workflow.Item{{ID: "1", Text: "Review implementation", Status: workflow.Running}}}
	m.handleEvent(parallelStart("agents",
		agent.ToolCallRef{ID: "a", Name: "agent", Args: map[string]any{"prompt": "one"}},
		agent.ToolCallRef{ID: "b", Name: "agent", Args: map[string]any{"prompt": "two"}},
	))
	out := plain(m.statusLine())
	if !strings.Contains(out, "1/1 Review implementation · 2 subagents in parallel · 3s") {
		t.Fatalf("status priority = %q", out)
	}
}
