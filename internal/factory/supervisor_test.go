package factory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scriptedAgent is a StepAgent whose behavior per step name is a function.
// It lets tests drive the supervisor without a real LLM.
type scriptedAgent struct {
	run func(ctx context.Context, step Step, dir, input string, nodeID int, events chan<- AgentEvent) (string, error)
}

func (f scriptedAgent) RunAgentStep(ctx context.Context, step Step, dir, input string, nodeID int, events chan<- AgentEvent) (string, error) {
	return f.run(ctx, step, dir, input, nodeID, events)
}

func testBudget() Budget {
	return Budget{MaxDepth: 3, MaxChildren: 8, MaxAgents: 20,
		MaxWall: time.Minute, ScriptTimeout: 10 * time.Second}
}

func newTestSupervisor(t *testing.T, agent StepAgent, b Budget, stepFiles map[string]string) (*Supervisor, string) {
	t.Helper()
	dir := t.TempDir()
	stepsDir := filepath.Join(dir, "steps")
	for name, content := range stepFiles {
		mode := os.FileMode(0o644)
		if !strings.HasSuffix(name, ".md") {
			mode = 0o755
		}
		writeFile(t, filepath.Join(stepsDir, name), content, mode)
	}
	return NewSupervisor(agent, b, Resolver{Paths: []string{stepsDir}}), dir
}

func TestRunScriptStep(t *testing.T) {
	sup, dir := newTestSupervisor(t, nil, testBudget(), map[string]string{
		"checks": "#!/bin/sh\nread -r line\necho \"got: $line\"\n",
	})
	res := sup.RunStep(context.Background(), 0, dir, "checks", "PR 42")
	if !res.Ok || res.Kind != "script" || !strings.Contains(res.Output, "got: PR 42") {
		t.Fatalf("script result: %+v", res)
	}
}

func TestRunScriptStepFailureExitCode(t *testing.T) {
	sup, dir := newTestSupervisor(t, nil, testBudget(), map[string]string{
		"fail": "#!/bin/sh\necho boom\nexit 3\n",
	})
	res := sup.RunStep(context.Background(), 0, dir, "fail", "")
	if res.Ok || !strings.Contains(res.Output, "boom") {
		t.Fatalf("want ok=false with output, got %+v", res)
	}
}

func TestRunScriptStepTimeout(t *testing.T) {
	b := testBudget()
	b.ScriptTimeout = 100 * time.Millisecond
	sup, dir := newTestSupervisor(t, nil, b, map[string]string{
		"slow": "#!/bin/sh\nsleep 5\n",
	})
	res := sup.RunStep(context.Background(), 0, dir, "slow", "")
	if res.Ok || !strings.Contains(res.Output, "timed out") {
		t.Fatalf("want timeout failure, got %+v", res)
	}
}

func TestMissingStepIsLegibleDenial(t *testing.T) {
	sup, dir := newTestSupervisor(t, nil, testBudget(), nil)
	res := sup.RunStep(context.Background(), 0, dir, "nonexistent", "")
	if res.Ok || res.Kind != "missing" || !strings.Contains(res.Output, "nonexistent") {
		t.Fatalf("missing step result: %+v", res)
	}
}

func TestListBuiltin(t *testing.T) {
	sup, dir := newTestSupervisor(t, nil, testBudget(), map[string]string{"custom.md": "Hi."})
	res := sup.RunStep(context.Background(), 0, dir, "list", "")
	if !res.Ok || !strings.Contains(res.Output, "custom") || !strings.Contains(res.Output, "worker") {
		t.Fatalf("list result: %+v", res)
	}
}

// TestDepthCap drives an agent step that recursively delegates to itself and
// asserts the supervisor cuts it off at MaxDepth with a legible denial.
func TestDepthCap(t *testing.T) {
	b := testBudget()
	b.MaxDepth = 2
	var sup *Supervisor
	var deepest StepResult
	agent := scriptedAgent{run: func(ctx context.Context, step Step, dir, input string, nodeID int, events chan<- AgentEvent) (string, error) {
		res := sup.RunStep(ctx, nodeID, dir, "recurse", input)
		if !res.Ok {
			deepest = res
		}
		return "done", nil
	}}
	sup, dir := newTestSupervisor(t, agent, b, map[string]string{"recurse.md": "Recurse."})

	res := sup.RunStep(context.Background(), 0, dir, "recurse", "go")
	if !res.Ok {
		t.Fatalf("root step should complete: %+v", res)
	}
	if !strings.Contains(deepest.Output, "max depth") {
		t.Fatalf("want max-depth denial surfaced to the agent, got %+v", deepest)
	}
}

func TestChildrenCap(t *testing.T) {
	b := testBudget()
	b.MaxChildren = 2
	var sup *Supervisor
	var denied int
	agent := scriptedAgent{run: func(ctx context.Context, step Step, dir, input string, nodeID int, events chan<- AgentEvent) (string, error) {
		if input != "leaf" {
			for range 4 {
				if res := sup.RunStep(ctx, nodeID, dir, "child", "leaf"); !res.Ok {
					if !strings.Contains(res.Output, "max children") {
						t.Errorf("unexpected denial: %+v", res)
					}
					denied++
				}
			}
		}
		return "done", nil
	}}
	sup, dir := newTestSupervisor(t, agent, b, map[string]string{"child.md": "Child."})

	if res := sup.RunStep(context.Background(), 0, dir, "child", "root"); !res.Ok {
		t.Fatalf("root: %+v", res)
	}
	if denied != 2 {
		t.Fatalf("denied = %d, want 2", denied)
	}
}

func TestAgentCap(t *testing.T) {
	b := testBudget()
	b.MaxAgents = 1
	agent := scriptedAgent{run: func(ctx context.Context, step Step, dir, input string, nodeID int, events chan<- AgentEvent) (string, error) {
		return "done", nil
	}}
	sup, dir := newTestSupervisor(t, agent, b, map[string]string{"a.md": "A."})

	if res := sup.RunStep(context.Background(), 0, dir, "a", ""); !res.Ok {
		t.Fatalf("first agent step: %+v", res)
	}
	res := sup.RunStep(context.Background(), 0, dir, "a", "")
	if res.Ok || !strings.Contains(res.Output, "agent cap") {
		t.Fatalf("want agent-cap denial, got %+v", res)
	}
}

func TestSnapshotAttribution(t *testing.T) {
	var sup *Supervisor
	agent := scriptedAgent{run: func(ctx context.Context, step Step, dir, input string, nodeID int, events chan<- AgentEvent) (string, error) {
		if input == "root" {
			sup.RunStep(ctx, nodeID, dir, "a", "leaf")
		}
		events <- AgentEvent{Kind: "tool", Body: "bash: just test"}
		return "done", nil
	}}
	sup, dir := newTestSupervisor(t, agent, testBudget(), map[string]string{"a.md": "A."})

	sup.RunStep(context.Background(), 0, dir, "a", "root")
	views := sup.Snapshot()
	if len(views) != 2 {
		t.Fatalf("nodes = %d, want 2", len(views))
	}
	var root, leaf NodeView
	for _, v := range views {
		if v.Parent == 0 {
			root = v
		} else {
			leaf = v
		}
	}
	if leaf.Parent != root.ID || leaf.Depth != 1 {
		t.Fatalf("child not attributed to root: root=%+v leaf=%+v", root, leaf)
	}
	if !root.Done || !leaf.Done {
		t.Fatal("nodes not marked done")
	}
}

// TestEventLifecycle asserts the stream carries enough to rebuild the tree:
// every node frames its events with start and done/fail, attributed with
// parent, depth, and task.
func TestEventLifecycle(t *testing.T) {
	var sup *Supervisor
	agent := scriptedAgent{run: func(ctx context.Context, step Step, dir, input string, nodeID int, events chan<- AgentEvent) (string, error) {
		if input == "root" {
			sup.RunStep(ctx, nodeID, dir, "child", "leaf work")
		}
		return "done", nil
	}}
	sup, dir := newTestSupervisor(t, agent, testBudget(), map[string]string{
		"parent.md": "P.", "child.md": "C.",
	})
	if res := sup.RunStep(context.Background(), 0, dir, "parent", "root"); !res.Ok {
		t.Fatalf("run: %+v", res)
	}

	type frame struct{ start, end string }
	frames := map[string]*frame{}
	var childEv Event
	for {
		select {
		case ev := <-sup.Events:
			f := frames[ev.Step]
			if f == nil {
				f = &frame{}
				frames[ev.Step] = f
			}
			switch ev.Ev.Kind {
			case "start":
				f.start = ev.Ev.Kind
				if ev.Step == "child" {
					childEv = ev
				}
			case "done", "fail":
				f.end = ev.Ev.Kind
			}
		default:
			goto drained
		}
	}
drained:
	for _, step := range []string{"parent", "child"} {
		f := frames[step]
		if f == nil || f.start != "start" || f.end != "done" {
			t.Errorf("%s lifecycle = %+v, want start…done", step, f)
		}
	}
	if childEv.Parent == 0 || childEv.Depth != 1 || childEv.Task != "leaf work" {
		t.Errorf("child start envelope = %+v, want parent/depth/task attribution", childEv)
	}
}

func TestRunStepToolEnvelope(t *testing.T) {
	sup, dir := newTestSupervisor(t, nil, testBudget(), map[string]string{
		"checks": "#!/bin/sh\necho GREEN\n",
	})
	tool := RunStepTool{Sup: sup, CallerNode: 0, Dir: dir}
	out, err := tool.Run(context.Background(), map[string]any{"name": "checks", "input": "42"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"ok":true`) || !strings.Contains(out, "GREEN") {
		t.Fatalf("tool output: %q", out)
	}
	if _, err := tool.Run(context.Background(), map[string]any{}); err == nil {
		t.Fatal("missing name should error")
	}
}
