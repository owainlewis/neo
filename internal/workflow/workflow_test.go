package workflow

import (
	"context"
	"fmt"
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

// fakeResolver returns canned step definitions by name. Avoids needing
// internal/config in these engine tests.
type fakeResolver struct {
	steps map[string]phase.Definition
}

func (f *fakeResolver) ResolveStep(name string) (phase.Definition, error) {
	if d, ok := f.steps[name]; ok {
		return d, nil
	}
	return phase.Definition{}, fmt.Errorf("step %q not found", name)
}

// testHarness sets up a fake step resolver, an artifact store, a fake
// provider, and an engine wired together.
func testHarness(t *testing.T, stepNames []string, responses []llm.Response) (*Engine, *recordingSink, *llmtest.FakeProvider) {
	t.Helper()
	dir := t.TempDir()

	resolver := &fakeResolver{steps: map[string]phase.Definition{}}
	for _, name := range stepNames {
		resolver.steps[name] = phase.Definition{
			Name:   name,
			Prompt: "step prompt for " + name,
			Source: "test",
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
		Resolver: resolver,
		Runner:   pr,
		Store:    store,
		Sink:     sink,
	}
	return eng, sink, prov
}

type recordingSink struct {
	events []Event
	agent  []agentEntry
}

type agentEntry struct {
	step string
	ev   agent.Event
}

func (r *recordingSink) OnWorkflow(e Event)              { r.events = append(r.events, e) }
func (r *recordingSink) OnAgent(s string, e agent.Event) { r.agent = append(r.agent, agentEntry{s, e}) }
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
		Name: "demo", Steps: []string{"build", "review"}, MaxRounds: 1,
	}, "do the thing")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	want := []EventKind{
		WorkflowStarted,
		StepStarted, StepCompleted,
		StepStarted, StepCompleted,
		WorkflowCompleted,
	}
	if got := sink.kinds(); !equalKinds(got, want) {
		t.Fatalf("event kinds:\n got:  %v\n want: %v", got, want)
	}
	if sink.events[1].Step != "build" || sink.events[3].Step != "review" {
		t.Fatalf("step names not propagated: %+v", sink.events)
	}
}

func TestEngine_RetryFromOnFailureMarker(t *testing.T) {
	// build "fails" on round 1 (output contains 'tests failed'), then succeeds
	// on round 2 along with review.
	eng, sink, _ := testHarness(t,
		[]string{"build", "review"},
		[]llm.Response{
			llmtest.Text("tests failed during smoke check"),
			llmtest.Text("build ok"),
			llmtest.Text("review ok"),
		},
	)

	err := eng.Run(context.Background(), Definition{
		Name: "demo", Steps: []string{"build", "review"},
		RetryFrom: "build", MaxRounds: 2,
	}, "do the thing")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	kinds := sink.kinds()
	if kinds[len(kinds)-1] != WorkflowCompleted {
		t.Fatalf("expected workflow to complete on round 2, got %v", kinds)
	}
	var sawFail, sawRetry bool
	for _, e := range sink.events {
		if e.Kind == StepFailed {
			sawFail = true
		}
		if e.Kind == RoundRetrying {
			sawRetry = true
			if e.Round != 2 {
				t.Fatalf("RoundRetrying.Round = %d, want 2", e.Round)
			}
			// Sinks (e.g. the TUI block) need RetryFrom on the event so they
			// can reset every downstream step row before the retry runs.
			if e.Step != "build" {
				t.Fatalf("RoundRetrying.Step = %q, want %q (the RetryFrom step)", e.Step, "build")
			}
		}
	}
	if !sawFail || !sawRetry {
		t.Fatalf("expected StepFailed + RoundRetrying, got %v", kinds)
	}
}

func TestEngine_MaxRoundsExhausted(t *testing.T) {
	eng, sink, _ := testHarness(t,
		[]string{"build"},
		[]llm.Response{llmtest.Text("tests failed"), llmtest.Text("tests failed again")},
	)

	err := eng.Run(context.Background(), Definition{
		Name: "demo", Steps: []string{"build"},
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
	eng, sink, _ := testHarness(t,
		[]string{"build"},
		nil, // FakeProvider with no scripted response → returns an error
	)

	err := eng.Run(context.Background(), Definition{
		Name: "demo", Steps: []string{"build"}, MaxRounds: 1,
	}, "do the thing")
	if err == nil {
		t.Fatal("expected error from runner failure")
	}

	kinds := sink.kinds()
	if kinds[len(kinds)-1] != WorkflowFailed {
		t.Fatalf("expected WorkflowFailed last, got %v", kinds)
	}
	var sawStepFail bool
	for _, k := range kinds {
		if k == StepFailed {
			sawStepFail = true
		}
	}
	if !sawStepFail {
		t.Fatalf("expected StepFailed in event stream, got %v", kinds)
	}
}

func TestEngine_AgentEventsBubbleThroughSink(t *testing.T) {
	eng, sink, _ := testHarness(t,
		[]string{"build"},
		[]llm.Response{llmtest.Text("ok")},
	)

	if err := eng.Run(context.Background(), Definition{
		Name: "demo", Steps: []string{"build"}, MaxRounds: 1,
	}, "do the thing"); err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	if len(sink.agent) == 0 {
		t.Fatal("expected at least one agent event in sink, got none")
	}
	if sink.agent[0].step != "build" {
		t.Fatalf("agent event not tagged with step: %+v", sink.agent[0])
	}
}

func TestEngine_RestoresRunnerOnEventOnExit(t *testing.T) {
	eng, _, _ := testHarness(t,
		[]string{"build"},
		[]llm.Response{llmtest.Text("ok")},
	)
	var called bool
	original := func(_ string, _ agent.Event) { called = true }
	eng.Runner.OnEvent = original

	_ = eng.Run(context.Background(), Definition{
		Name: "demo", Steps: []string{"build"}, MaxRounds: 1,
	}, "task")

	if eng.Runner.OnEvent == nil {
		t.Fatal("Runner.OnEvent was not restored after Run")
	}
	eng.Runner.OnEvent("x", agent.Event{})
	if !called {
		t.Fatal("Runner.OnEvent was overwritten with something other than the original")
	}
}

func TestLoadFile_ParsesAgentAndCommandSteps(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "BUILD.md")
	if err := os.WriteFile(promptPath, []byte("build {{.Task}} {{.RunID}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	flowPath := filepath.Join(dir, "build-flow.yml")
	if err := os.WriteFile(flowPath, []byte(`
steps:
  - name: build
    type: agent
    prompt: BUILD.md
  - name: test
    type: command
    run: test -n "{{.RunID}}"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	def, err := LoadFile(flowPath)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if def.Name != "build-flow.yml" {
		t.Fatalf("name = %q", def.Name)
	}
	if got := def.StepNames(); !equalStrings(got, []string{"build", "test"}) {
		t.Fatalf("step names = %v", got)
	}
	if def.StepDefs[0].Kind != StepAgent || def.StepDefs[1].Kind != StepCommand {
		t.Fatalf("unexpected kinds: %+v", def.StepDefs)
	}
	if !strings.Contains(def.StepDefs[0].Prompt, "{{.Task}}") {
		t.Fatalf("prompt not loaded: %q", def.StepDefs[0].Prompt)
	}
}

func TestLoadFile_RepoSmokeExampleLoads(t *testing.T) {
	def, err := LoadFile(filepath.Join("..", "..", "examples", "neo-smoke-flow.yml"))
	if err != nil {
		t.Fatalf("LoadFile examples/neo-smoke-flow.yml: %v", err)
	}
	if got := def.StepNames(); !equalStrings(got, []string{"plan", "check-run-directory-exists", "check-plan-file-exists", "check-plan-mentions-smoke", "focused-tests", "summarize"}) {
		t.Fatalf("step names = %v", got)
	}
}

func TestEngine_FileFlowRunsAgentAndCommandSteps(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	promptPath := filepath.Join(dir, "BUILD.md")
	if err := os.WriteFile(promptPath, []byte("step={{.StepName}} run={{.RunID}} task={{.Task}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	flowPath := filepath.Join(dir, "build-flow.yml")
	if err := os.WriteFile(flowPath, []byte(`
steps:
  - name: build
    type: agent
    prompt: BUILD.md
  - name: check
    type: command
    run: test -n "{{.RunID}}"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	def, err := LoadFile(flowPath)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("built")}}
	pr := &phase.Runner{Provider: prov, Tools: tools.NewRegistry(), DefaultModel: "m"}
	sink := &recordingSink{}
	eng := &Engine{
		Runner: pr,
		Store:  artifact.NewStore(filepath.Join(dir, "runs")),
		Sink:   sink,
	}

	if err := eng.Run(context.Background(), def, "fix issue 21"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(prov.Calls) != 1 {
		t.Fatalf("expected one agent call, got %d", len(prov.Calls))
	}
	sys := prov.Calls[0].System
	for _, want := range []string{"step=build", "run=build-flow-yml-", "task=fix issue 21"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("agent prompt missing %q: %q", want, sys)
		}
	}
	if got := sink.kinds(); got[len(got)-1] != WorkflowCompleted {
		t.Fatalf("expected completion, got %v", got)
	}
	var sawCommand bool
	for _, ev := range sink.agent {
		if ev.step == "check" && ev.ev.Kind == agent.EventToolCall && ev.ev.Name == "bash" {
			sawCommand = true
		}
	}
	if !sawCommand {
		t.Fatalf("expected command step to emit bash activity, got %+v", sink.agent)
	}
}

func TestEngine_FileFlowCommandFailureStopsWorkflow(t *testing.T) {
	dir := t.TempDir()
	flowPath := filepath.Join(dir, "fail-flow.yml")
	if err := os.WriteFile(flowPath, []byte(`
steps:
  - name: fail
    type: command
    run: exit 7
  - name: never
    type: command
    run: echo nope
`), 0o644); err != nil {
		t.Fatal(err)
	}
	def, err := LoadFile(flowPath)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	sink := &recordingSink{}
	eng := &Engine{
		Runner: &phase.Runner{Provider: &llmtest.FakeProvider{}, Tools: tools.NewRegistry(), DefaultModel: "m"},
		Store:  artifact.NewStore(filepath.Join(dir, "runs")),
		Sink:   sink,
	}

	err = eng.Run(context.Background(), def, "task")
	if err == nil {
		t.Fatal("expected command failure")
	}
	if !strings.Contains(err.Error(), "exit 7") {
		t.Fatalf("expected exit code in error, got %v", err)
	}
	kinds := sink.kinds()
	if !equalKinds(kinds, []EventKind{WorkflowStarted, StepStarted, StepFailed, WorkflowFailed}) {
		t.Fatalf("unexpected events: %v", kinds)
	}
}

func TestEngine_FileFlowCommandCancellationReturnsContextCanceled(t *testing.T) {
	dir := t.TempDir()
	flowPath := filepath.Join(dir, "cancel-flow.yml")
	if err := os.WriteFile(flowPath, []byte(`
steps:
  - name: wait
    type: command
    run: sleep 5
`), 0o644); err != nil {
		t.Fatal(err)
	}
	def, err := LoadFile(flowPath)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	eng := &Engine{
		Runner: &phase.Runner{Provider: &llmtest.FakeProvider{}, Tools: tools.NewRegistry(), DefaultModel: "m"},
		Store:  artifact.NewStore(filepath.Join(dir, "runs")),
		Sink:   &recordingSink{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = eng.Run(ctx, def, "task")
	if err != context.Canceled {
		t.Fatalf("Run error = %v, want context.Canceled", err)
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

func equalStrings(a, b []string) bool {
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
