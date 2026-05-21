package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/artifact"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/phase"
	"github.com/owainlewis/neo/internal/tools"
)

// testHarness sets up a temp phases dir, an artifact store, a fake provider,
// and an engine wired together. Phase prompts default to a single placeholder
// line; per-phase override available via writePhase.
func testHarness(t *testing.T, phaseNames []string, responses []llm.Response) (*Engine, *recordingSink, *llmtest.FakeProvider) {
	t.Helper()
	dir := t.TempDir()
	phasesDir := filepath.Join(dir, "phases")
	if err := os.MkdirAll(phasesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range phaseNames {
		if err := os.WriteFile(filepath.Join(phasesDir, name+".md"), []byte("phase prompt"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	prov := &llmtest.FakeProvider{Responses: responses}
	pr := &phase.Runner{
		Provider:     prov,
		Tools:        tools.NewRegistry(),
		DefaultModel: "test-model",
	}
	store := artifact.NewStore(filepath.Join(dir, "runs"))
	sink := &recordingSink{}
	eng := &Engine{
		PhasesDir: phasesDir,
		Runner:    pr,
		Store:     store,
		Sink:      sink,
	}
	return eng, sink, prov
}

type recordingSink struct {
	events []Event
	agent  []agentEntry
}

type agentEntry struct {
	phase string
	ev    agent.Event
}

func (r *recordingSink) OnWorkflow(e Event)                  { r.events = append(r.events, e) }
func (r *recordingSink) OnAgent(p string, e agent.Event)     { r.agent = append(r.agent, agentEntry{p, e}) }
func (r *recordingSink) kinds() []EventKind {
	out := make([]EventKind, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, e.Kind)
	}
	return out
}

func TestEngine_HappyPath(t *testing.T) {
	eng, sink, _ := testHarness(t,
		[]string{"build", "review"},
		[]llm.Response{llmtest.Text("build ok"), llmtest.Text("review ok")},
	)

	err := eng.Run(context.Background(), Definition{
		Name: "demo", Phases: []string{"build", "review"}, MaxRounds: 1,
	}, "do the thing")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	want := []EventKind{
		WorkflowStarted,
		PhaseStarted, PhaseCompleted,
		PhaseStarted, PhaseCompleted,
		WorkflowCompleted,
	}
	if got := sink.kinds(); !equalKinds(got, want) {
		t.Fatalf("event kinds:\n got:  %v\n want: %v", got, want)
	}
	// Phase events are tagged correctly.
	if sink.events[1].Phase != "build" || sink.events[3].Phase != "review" {
		t.Fatalf("phase names not propagated: %+v", sink.events)
	}
}

func TestEngine_RetryFromOnFailureMarker(t *testing.T) {
	// build "fails" on round 1 (output contains 'tests failed'), then succeeds
	// on round 2 along with review.
	eng, sink, _ := testHarness(t,
		[]string{"build", "review"},
		[]llm.Response{
			llmtest.Text("tests failed during smoke check"), // round 1 build → triggers retry
			llmtest.Text("build ok"),                        // round 2 build
			llmtest.Text("review ok"),                       // round 2 review
		},
	)

	err := eng.Run(context.Background(), Definition{
		Name: "demo", Phases: []string{"build", "review"},
		RetryFrom: "build", MaxRounds: 2,
	}, "do the thing")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	kinds := sink.kinds()
	if kinds[len(kinds)-1] != WorkflowCompleted {
		t.Fatalf("expected workflow to complete on round 2, got %v", kinds)
	}
	// Must have at least one PhaseFailed and one RoundRetrying.
	var sawFail, sawRetry bool
	for _, e := range sink.events {
		if e.Kind == PhaseFailed {
			sawFail = true
		}
		if e.Kind == RoundRetrying {
			sawRetry = true
			if e.Round != 2 {
				t.Fatalf("RoundRetrying.Round = %d, want 2", e.Round)
			}
			// Sink consumers (e.g. the TUI workflow block) need RetryFrom on
			// the event so they can reset every downstream phase row before
			// the retry round runs. Without this they only reset the failed
			// row and downstream completed rows stay rendered as ✓.
			if e.Phase != "build" {
				t.Fatalf("RoundRetrying.Phase = %q, want %q (the RetryFrom phase)", e.Phase, "build")
			}
		}
	}
	if !sawFail || !sawRetry {
		t.Fatalf("expected PhaseFailed + RoundRetrying, got %v", kinds)
	}
}

func TestEngine_MaxRoundsExhausted(t *testing.T) {
	// Every round of build fails. MaxRounds=2 → one retry, then give up.
	eng, sink, _ := testHarness(t,
		[]string{"build"},
		[]llm.Response{
			llmtest.Text("tests failed"),
			llmtest.Text("tests failed again"),
		},
	)

	err := eng.Run(context.Background(), Definition{
		Name: "demo", Phases: []string{"build"},
		RetryFrom: "build", MaxRounds: 2,
	}, "do the thing")
	if err == nil {
		t.Fatal("expected workflow to fail after max rounds")
	}
	if !strings.Contains(err.Error(), "max rounds") &&
		!strings.Contains(err.Error(), "failed") {
		t.Fatalf("unexpected error: %v", err)
	}

	last := sink.events[len(sink.events)-1]
	if last.Kind != WorkflowFailed {
		t.Fatalf("expected last event WorkflowFailed, got %s", last.Kind)
	}
}

func TestEngine_RunnerErrorStopsWorkflow(t *testing.T) {
	// Provider errors out (no scripted response) → phase.Runner returns an
	// error → engine surfaces PhaseFailed + WorkflowFailed and stops.
	eng, sink, _ := testHarness(t,
		[]string{"build"},
		nil, // no responses → FakeProvider returns an error
	)

	err := eng.Run(context.Background(), Definition{
		Name: "demo", Phases: []string{"build"}, MaxRounds: 1,
	}, "do the thing")
	if err == nil {
		t.Fatal("expected error from runner failure")
	}

	kinds := sink.kinds()
	if kinds[len(kinds)-1] != WorkflowFailed {
		t.Fatalf("expected WorkflowFailed last, got %v", kinds)
	}
	// PhaseFailed must precede WorkflowFailed.
	var sawPhaseFail bool
	for _, k := range kinds {
		if k == PhaseFailed {
			sawPhaseFail = true
		}
	}
	if !sawPhaseFail {
		t.Fatalf("expected PhaseFailed in event stream, got %v", kinds)
	}
}

func TestEngine_AgentEventsBubbleThroughSink(t *testing.T) {
	// One text-only response: the agent should emit an EventAssistantText
	// during the run, and that should land in sink.agent tagged with the
	// phase name.
	eng, sink, _ := testHarness(t,
		[]string{"build"},
		[]llm.Response{llmtest.Text("ok")},
	)

	if err := eng.Run(context.Background(), Definition{
		Name: "demo", Phases: []string{"build"}, MaxRounds: 1,
	}, "do the thing"); err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	if len(sink.agent) == 0 {
		t.Fatal("expected at least one agent event in sink, got none")
	}
	if sink.agent[0].phase != "build" {
		t.Fatalf("agent event not tagged with phase: %+v", sink.agent[0])
	}
}

func TestEngine_RestoresRunnerOnEventOnExit(t *testing.T) {
	// Engine.Run takes over Runner.OnEvent during execution. After Run
	// returns, the original callback must be restored even on failure.
	eng, _, _ := testHarness(t,
		[]string{"build"},
		[]llm.Response{llmtest.Text("ok")},
	)
	var called bool
	original := func(_ string, _ agent.Event) { called = true }
	eng.Runner.OnEvent = original

	_ = eng.Run(context.Background(), Definition{
		Name: "demo", Phases: []string{"build"}, MaxRounds: 1,
	}, "task")

	if eng.Runner.OnEvent == nil {
		t.Fatal("Runner.OnEvent was not restored after Run")
	}
	eng.Runner.OnEvent("x", agent.Event{})
	if !called {
		t.Fatal("Runner.OnEvent was overwritten with something other than the original")
	}
}

func equalKinds(a, b []EventKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
