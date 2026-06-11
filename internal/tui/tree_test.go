package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/factory"
)

// renderPlain renders a block with ANSI styling stripped, so tests can
// assert on visible content.
func renderPlain(b block, width int) string {
	return ansi.Strip(b.render(width, nil))
}

func stepEv(node, parent int, step, kind, body, task string) factory.Event {
	depth := 0
	if parent != 0 {
		depth = 1
	}
	return factory.Event{Node: node, Parent: parent, Depth: depth, Step: step, Task: task,
		Ev: factory.AgentEvent{Kind: kind, Body: body}}
}

func TestTreeBuildsStepsAndSubSteps(t *testing.T) {
	m := makeTestModel()

	// ship starts, spawns checks (script) and verify (agent), all finish.
	m.handleStepEvent(stepEv(1, 0, "ship", "start", "", "add rate limiting"))
	m.handleStepEvent(stepEv(2, 1, "checks", "start", "", "34"))
	m.handleStepEvent(stepEv(2, 1, "checks", "done", "ALL CHECKS GREEN", "34"))
	m.handleStepEvent(stepEv(3, 1, "verify", "start", "", "branch vs criteria"))
	m.handleStepEvent(stepEv(3, 1, "verify", "tool", "bash: just test", "branch vs criteria"))

	if len(m.blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(m.blocks))
	}
	tb, ok := m.blocks[0].(*treeBlock)
	if !ok {
		t.Fatalf("block = %T", m.blocks[0])
	}
	out := renderPlain(tb, 100)
	for _, want := range []string{
		"● ship", "add rate limiting",
		"├─ ✓ checks",
		"└─ ● verify",
		"└ bash: just test",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tree missing %q:\n%s", want, out)
		}
	}

	// Children settle, then the root: glyphs update, status line clears.
	m.handleStepEvent(stepEv(3, 1, "verify", "done", "VERDICT: PASS", ""))
	m.handleStepEvent(stepEv(1, 0, "ship", "done", "shipped", ""))
	out = renderPlain(tb, 100)
	for _, want := range []string{"✓ ship", "└─ ✓ verify"} {
		if !strings.Contains(out, want) {
			t.Errorf("settled tree missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "just test") {
		t.Errorf("status line should clear on completion:\n%s", out)
	}
	if tb.running() {
		t.Error("block still reports running")
	}
}

func TestTreeFailureGlyph(t *testing.T) {
	m := makeTestModel()
	m.handleStepEvent(stepEv(1, 0, "verify", "start", "", "PR #9"))
	m.handleStepEvent(stepEv(1, 0, "verify", "fail", "agent step error: timeout", ""))

	out := renderPlain(m.blocks[0].(*treeBlock), 100)
	if !strings.Contains(out, "✗ verify") {
		t.Errorf("missing failure glyph:\n%s", out)
	}
}

func TestTreeGroupsConsecutiveRootsAndSplitsOnText(t *testing.T) {
	m := makeTestModel()

	m.handleStepEvent(stepEv(1, 0, "checks", "start", "", ""))
	m.handleStepEvent(stepEv(1, 0, "checks", "done", "GREEN", ""))
	m.handleStepEvent(stepEv(2, 0, "explore", "start", "", "where are budgets"))
	if len(m.blocks) != 1 {
		t.Fatalf("consecutive roots should share a block, got %d blocks", len(m.blocks))
	}
	if got := len(m.blocks[0].(*treeBlock).roots); got != 2 {
		t.Fatalf("roots = %d, want 2", got)
	}

	m.handleStepEvent(stepEv(2, 0, "explore", "done", "answer", ""))
	m.handleEvent(agent.Event{Kind: agent.EventAssistantText, Text: "both done, now verifying"})
	m.handleStepEvent(stepEv(3, 0, "verify", "start", "", ""))

	// tree, text, tree
	if len(m.blocks) != 3 {
		t.Fatalf("blocks = %d, want 3", len(m.blocks))
	}
	if m.blocks[0] == m.blocks[2] {
		t.Fatal("text between calls must split tree blocks")
	}
}

func TestTreeRunStepResultSuppressedOnSuccessCardOnFailure(t *testing.T) {
	m := makeTestModel()

	// Success: the tree is the record; no result card.
	m.handleStepEvent(stepEv(1, 0, "checks", "start", "", ""))
	m.handleStepEvent(stepEv(1, 0, "checks", "done", "GREEN", ""))
	m.handleEvent(agent.Event{Kind: agent.EventToolResult, Name: "run_step",
		Text: "{\"ok\":true,\"kind\":\"script\",\"took\":\"1s\"}\nGREEN"})
	if len(m.blocks) != 1 {
		t.Fatalf("success should add no card: %d blocks", len(m.blocks))
	}

	// Denial: no node ever started, so the error card is the only record.
	m.handleEvent(agent.Event{Kind: agent.EventToolResult, Name: "run_step",
		Text: "{\"ok\":false,\"kind\":\"agent\",\"took\":\"0s\"}\ndenied: max depth"})
	card, ok := m.blocks[len(m.blocks)-1].(toolResultBlock)
	if !ok || !card.isError || !strings.Contains(card.text, "denied: max depth") {
		t.Fatalf("expected error card, got %#v", m.blocks[len(m.blocks)-1])
	}
}

func TestTreeRunStepCallEmitsNoGenericCard(t *testing.T) {
	m := makeTestModel()
	m.handleEvent(agent.Event{Kind: agent.EventToolCall, Name: "run_step",
		Args: map[string]any{"name": "checks", "input": ""}})
	if len(m.blocks) != 0 {
		t.Fatalf("run_step call should not render a tool card: %d blocks", len(m.blocks))
	}
	if m.currentTool == nil || m.currentTool.name != "run_step" {
		t.Fatal("status line should still track the in-flight call")
	}
}

func TestTreeOrphanEventsIgnored(t *testing.T) {
	m := makeTestModel()
	m.handleStepEvent(stepEv(9, 4, "verify", "start", "", "")) // unknown parent
	m.handleStepEvent(stepEv(9, 4, "verify", "tool", "x", ""))
	if len(m.blocks) != 0 {
		t.Fatalf("orphan events must not create blocks: %d", len(m.blocks))
	}
}

func TestTreeElapsedUsesNodeClock(t *testing.T) {
	m := makeTestModel()
	m.handleStepEvent(stepEv(1, 0, "worker", "start", "", "#12"))
	tb := m.blocks[0].(*treeBlock)
	tb.nodes[1].startAt = time.Now().Add(-90 * time.Second)
	if out := renderPlain(tb, 100); !strings.Contains(out, "1m30s") {
		t.Errorf("running elapsed not live:\n%s", out)
	}
}
