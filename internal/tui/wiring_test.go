package tui

import (
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/workflow"
)

// recordingSend captures messages the sink would push to the program.
type recordingSend struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (r *recordingSend) send(m tea.Msg) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, m)
}

func TestTuiSink_ForwardsWorkflowEvents(t *testing.T) {
	rec := &recordingSend{}
	s := &tuiSink{send: rec.send}
	want := workflow.Event{Kind: workflow.PhaseStarted, Phase: "build", Round: 1, Index: 1, Total: 2}
	s.OnWorkflow(want)

	if len(rec.msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(rec.msgs))
	}
	got, ok := rec.msgs[0].(workflowEventMsg)
	if !ok {
		t.Fatalf("expected workflowEventMsg, got %T", rec.msgs[0])
	}
	if got.ev != want {
		t.Fatalf("event mismatch: got %+v want %+v", got.ev, want)
	}
}

func TestTuiSink_ForwardsAgentEventsWithPhase(t *testing.T) {
	rec := &recordingSend{}
	s := &tuiSink{send: rec.send}
	s.OnAgent("build", agent.Event{Kind: agent.EventToolCall, Name: "bash"})

	got, ok := rec.msgs[0].(workflowAgentEventMsg)
	if !ok {
		t.Fatalf("expected workflowAgentEventMsg, got %T", rec.msgs[0])
	}
	if got.phase != "build" {
		t.Fatalf("phase: got %q want build", got.phase)
	}
	if got.ev.Name != "bash" {
		t.Fatalf("agent event not preserved: %+v", got.ev)
	}
}

// makeTestModel builds a minimal model suitable for slash-command and
// state-transition tests without going through newModel (which probes the
// terminal). Only the fields exercised here are populated.
func makeTestModel(t *testing.T) *model {
	t.Helper()
	return &model{
		send: func(tea.Msg) {},
	}
}

func TestSlashCommand_RunWithoutFlowEmitsError(t *testing.T) {
	m := makeTestModel(t)
	m.handleSlashCommand("/run")

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "usage") {
		t.Fatalf("error should describe usage, got %v", eb.err)
	}
}

func TestSlashCommand_CancelWithoutActiveWorkflowEmitsError(t *testing.T) {
	m := makeTestModel(t)
	m.handleSlashCommand("/cancel")

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "no workflow") {
		t.Fatalf("unexpected error: %v", eb.err)
	}
}

func TestSlashCommand_UnknownEmitsError(t *testing.T) {
	m := makeTestModel(t)
	m.handleSlashCommand("/wat")

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "unknown") {
		t.Fatalf("unexpected error: %v", eb.err)
	}
}

// Regression: /cancel was unreachable while a workflow was running because
// the enter handler short-circuited on m.activeWorkflow != nil before slash
// commands were parsed.
func TestSlashCommand_CancelWorksWhileWorkflowActive(t *testing.T) {
	m := makeTestModel(t)
	cancelled := false
	m.activeWorkflow = newWorkflowBlock("demo", "x", []string{"build"}, 1)
	m.workflowCancel = func() { cancelled = true }

	m.handleSlashCommand("/cancel")
	if !cancelled {
		t.Fatal("/cancel did not invoke workflowCancel")
	}
}

func TestSlashCommand_RunMissingFlowEmitsError(t *testing.T) {
	// /run names a flow that doesn't exist; loadDefinition returns an error.
	m := makeTestModel(t)
	m.wf = WorkflowConfig{FlowsDir: t.TempDir()}
	m.handleSlashCommand("/run nonexistent some task")

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "load flow") {
		t.Fatalf("error should reference load failure, got %v", eb.err)
	}
}
