package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/workflow"
)

// stripANSI removes ANSI escape sequences so render output can be asserted
// against plain text. lipgloss v2 styles render inline ANSI; tests don't care
// about colour, only structure.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

func newTestBlock(t *testing.T, phases ...string) *workflowBlock {
	t.Helper()
	return newWorkflowBlock("implementation", "do the thing", phases, 3)
}

func TestWorkflowBlock_InitialRenderShowsAllPending(t *testing.T) {
	b := newTestBlock(t, "build", "eval", "finalize")
	out := plain(b.render(80, nil))

	for _, want := range []string{
		"implementation",
		"do the thing",
		"○ build",
		"○ eval",
		"○ finalize",
		"1/3", "2/3", "3/3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "✓") || strings.Contains(out, "▶") {
		t.Errorf("expected no completed/active glyphs yet, got:\n%s", out)
	}
}

func TestWorkflowBlock_PhaseStartedMarksActive(t *testing.T) {
	b := newTestBlock(t, "build", "eval")
	b.Apply(workflow.Event{Kind: workflow.PhaseStarted, Phase: "build", Round: 1, Index: 1, Total: 2})

	out := plain(b.render(80, nil))
	if !strings.Contains(out, "▶ build") {
		t.Fatalf("expected ▶ build, got:\n%s", out)
	}
	if !strings.Contains(out, "○ eval") {
		t.Fatalf("expected eval to remain pending, got:\n%s", out)
	}
}

func TestWorkflowBlock_PhaseCompletedShowsDuration(t *testing.T) {
	b := newTestBlock(t, "build")
	b.Apply(workflow.Event{Kind: workflow.PhaseStarted, Phase: "build"})
	// Simulate elapsed time by rewriting the start timestamp.
	b.phases[0].started = time.Now().Add(-1500 * time.Millisecond)
	b.Apply(workflow.Event{Kind: workflow.PhaseCompleted, Phase: "build"})

	out := plain(b.render(80, nil))
	if !strings.Contains(out, "✓ build") {
		t.Fatalf("expected ✓ build, got:\n%s", out)
	}
	// Duration formatting is "1.5s" via fmtElapsed.
	if !strings.Contains(out, "1.5s") && !strings.Contains(out, "1s") {
		t.Fatalf("expected duration in row, got:\n%s", out)
	}
}

func TestWorkflowBlock_PhaseFailedShowsMessage(t *testing.T) {
	b := newTestBlock(t, "build")
	b.Apply(workflow.Event{Kind: workflow.PhaseStarted, Phase: "build"})
	b.Apply(workflow.Event{Kind: workflow.PhaseFailed, Phase: "build", Message: "tests failed"})

	out := plain(b.render(80, nil))
	if !strings.Contains(out, "✗ build") {
		t.Fatalf("expected ✗ build, got:\n%s", out)
	}
	if !strings.Contains(out, "tests failed") {
		t.Fatalf("expected failure message, got:\n%s", out)
	}
}

func TestWorkflowBlock_RoundRetryingResetsFailedPhases(t *testing.T) {
	b := newTestBlock(t, "build", "eval")
	b.Apply(workflow.Event{Kind: workflow.PhaseStarted, Phase: "build"})
	b.Apply(workflow.Event{Kind: workflow.PhaseFailed, Phase: "build", Message: "broke"})
	// No Phase on the event → fall back to "only reset failed".
	b.Apply(workflow.Event{Kind: workflow.RoundRetrying, Round: 2})

	if b.round != 2 {
		t.Fatalf("round = %d, want 2", b.round)
	}
	if b.phases[0].status != phasePending {
		t.Fatalf("failed phase not reset to pending: %+v", b.phases[0])
	}
	if b.phases[0].message != "" {
		t.Fatalf("failed phase message not cleared: %q", b.phases[0].message)
	}

	out := plain(b.render(80, nil))
	if !strings.Contains(out, "round 2/3") {
		t.Fatalf("expected 'round 2/3' in header, got:\n%s", out)
	}
	if strings.Contains(out, "broke") {
		t.Fatalf("expected reset to clear failure message, got:\n%s", out)
	}
}

// When RoundRetrying carries the retry-from phase name, every phase from
// that index onward is reset — including ones that previously completed.
// Without this, downstream phases would still render as ✓ even though the
// retry round is about to re-execute them.
func TestWorkflowBlock_RoundRetryingResetsAllPhasesFromRetryFrom(t *testing.T) {
	b := newTestBlock(t, "build", "eval", "finalize")
	// Round 1: build and eval complete; finalize fails.
	b.Apply(workflow.Event{Kind: workflow.PhaseStarted, Phase: "build"})
	b.Apply(workflow.Event{Kind: workflow.PhaseCompleted, Phase: "build"})
	b.Apply(workflow.Event{Kind: workflow.PhaseStarted, Phase: "eval"})
	b.Apply(workflow.Event{Kind: workflow.PhaseCompleted, Phase: "eval"})
	b.Apply(workflow.Event{Kind: workflow.PhaseStarted, Phase: "finalize"})
	b.Apply(workflow.Event{Kind: workflow.PhaseFailed, Phase: "finalize", Message: "broke"})

	// Engine retries from "build" — every phase will run again.
	b.Apply(workflow.Event{Kind: workflow.RoundRetrying, Phase: "build", Round: 2})

	for i, p := range b.phases {
		if p.status != phasePending {
			t.Errorf("phase[%d] %q not reset (status=%v) — should be pending after retry-from build", i, p.name, p.status)
		}
	}
}

func TestWorkflowBlock_WorkflowCompletedShowsSummary(t *testing.T) {
	b := newTestBlock(t, "build")
	b.startedAt = time.Now().Add(-5 * time.Second)
	b.Apply(workflow.Event{Kind: workflow.WorkflowCompleted})

	out := plain(b.render(80, nil))
	if !strings.Contains(out, "✓ completed") {
		t.Fatalf("expected ✓ completed in summary, got:\n%s", out)
	}
	// Duration of at least 5s should appear.
	if !strings.Contains(out, "5s") {
		t.Fatalf("expected 5s duration in summary, got:\n%s", out)
	}
}

func TestWorkflowBlock_WorkflowFailedShowsReason(t *testing.T) {
	b := newTestBlock(t, "build")
	b.Apply(workflow.Event{Kind: workflow.WorkflowFailed, Message: "max rounds (3) reached"})

	out := plain(b.render(80, nil))
	if !strings.Contains(out, "✗ failed") {
		t.Fatalf("expected ✗ failed, got:\n%s", out)
	}
	if !strings.Contains(out, "max rounds") {
		t.Fatalf("expected failure reason, got:\n%s", out)
	}
}

func TestWorkflowBlock_AgentEventsUpdateActivePhaseDetail(t *testing.T) {
	b := newTestBlock(t, "build")
	b.Apply(workflow.Event{Kind: workflow.PhaseStarted, Phase: "build"})

	// Tool call appears in the active row's detail column.
	b.ApplyAgent("build", agent.Event{
		Kind: agent.EventToolCall,
		Name: "bash",
		Args: map[string]any{"command": "go test ./..."},
	})

	out := plain(b.render(80, nil))
	if !strings.Contains(out, "running go test") {
		t.Fatalf("expected 'running go test' in active row, got:\n%s", out)
	}

	// Tool result clears the detail back to (implicit) thinking.
	b.ApplyAgent("build", agent.Event{Kind: agent.EventToolResult, Name: "bash"})
	if b.detail != "" {
		t.Fatalf("expected detail to clear after tool result, got %q", b.detail)
	}
}

func TestWorkflowBlock_AgentEventsForInactivePhaseIgnored(t *testing.T) {
	b := newTestBlock(t, "build", "eval")
	b.Apply(workflow.Event{Kind: workflow.PhaseStarted, Phase: "build"})

	// Event tagged for a non-active phase must not change the detail.
	b.ApplyAgent("eval", agent.Event{
		Kind: agent.EventToolCall,
		Name: "bash",
		Args: map[string]any{"command": "should not appear"},
	})
	if b.detail != "" {
		t.Fatalf("detail mutated by event for inactive phase: %q", b.detail)
	}
}

func TestWorkflowBlock_FullRunSequence(t *testing.T) {
	// End-to-end: drive a realistic event sequence and confirm the final
	// rendered output reads like the Pi reference (structure-wise).
	b := newTestBlock(t, "build", "eval", "finalize")

	b.Apply(workflow.Event{Kind: workflow.WorkflowStarted, Round: 1, Total: 3})
	for i, name := range []string{"build", "eval", "finalize"} {
		b.Apply(workflow.Event{Kind: workflow.PhaseStarted, Phase: name, Round: 1, Index: i + 1, Total: 3})
		b.phases[i].started = time.Now().Add(-time.Second)
		b.Apply(workflow.Event{Kind: workflow.PhaseCompleted, Phase: name, Round: 1, Index: i + 1, Total: 3})
	}
	b.Apply(workflow.Event{Kind: workflow.WorkflowCompleted})

	out := plain(b.render(80, nil))

	// Three completed phases + completed summary.
	if got := strings.Count(out, "✓ "); got < 4 { // 3 phases + 1 summary
		t.Fatalf("expected at least 4 ✓ markers, got %d in:\n%s", got, out)
	}
	if strings.Contains(out, "▶") || strings.Contains(out, "○") {
		t.Fatalf("expected no active/pending markers in completed run, got:\n%s", out)
	}
}
