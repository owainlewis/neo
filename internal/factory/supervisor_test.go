package factory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/tools"
)

type scriptedRunner struct {
	run func(context.Context, string, string, chan<- AgentEvent) (string, error)
}

func (r scriptedRunner) RunAgent(ctx context.Context, dir, input string, events chan<- AgentEvent) (string, error) {
	return r.run(ctx, dir, input, events)
}

func (r scriptedRunner) RunAgentWithOptions(ctx context.Context, dir, input string, events chan<- AgentEvent, _ RunOptions) (string, error) {
	return r.run(ctx, dir, input, events)
}

type configuredScriptedRunner struct {
	run func(context.Context, string, string, chan<- AgentEvent, RunOptions) (string, error)
}

var _ Runner = configuredScriptedRunner{}

func (r configuredScriptedRunner) RunAgent(ctx context.Context, dir, input string, events chan<- AgentEvent) (string, error) {
	return r.run(ctx, dir, input, events, RunOptions{})
}

func (r configuredScriptedRunner) RunAgentWithOptions(ctx context.Context, dir, input string, events chan<- AgentEvent, opts RunOptions) (string, error) {
	return r.run(ctx, dir, input, events, opts)
}

func testBudget() Budget {
	return Budget{MaxAgents: 8, MaxWall: time.Second}
}

func newTestSupervisor(t *testing.T, run func(context.Context, string, string, chan<- AgentEvent) (string, error), budget Budget) (*Supervisor, string) {
	t.Helper()
	return NewSupervisor(scriptedRunner{run: run}, budget), t.TempDir()
}

func TestAgentCapBoundsParallelDelegation(t *testing.T) {
	budget := testBudget()
	budget.MaxAgents = 1
	started := make(chan struct{})
	release := make(chan struct{})
	sup, dir := newTestSupervisor(t, func(context.Context, string, string, chan<- AgentEvent) (string, error) {
		close(started)
		<-release
		return "done", nil
	}, budget)

	first := make(chan AgentResult, 1)
	go func() { first <- sup.RunAgentPrompt(context.Background(), dir, "review") }()
	<-started
	second := sup.RunAgentPrompt(context.Background(), dir, "research")
	close(release)

	if res := <-first; !res.Ok {
		t.Fatalf("first=%+v", res)
	}
	if second.Ok || !strings.Contains(second.Output, "agent cap") {
		t.Fatalf("second=%+v", second)
	}
}

func TestEventLifecycleCarriesCallMetadata(t *testing.T) {
	sup, dir := newTestSupervisor(t, func(_ context.Context, _ string, _ string, events chan<- AgentEvent) (string, error) {
		events <- AgentEvent{Kind: "tool", Body: "read main.go"}
		return "done", nil
	}, testBudget())
	call := tools.CallMetadata{ToolUseID: "call-a", GroupID: "group", GroupSize: 2, GroupPos: 1}

	if res := sup.RunAgentPrompt(context.Background(), dir, "review", PromptOptions{Mode: AgentModeInspect, Call: call}); !res.Ok {
		t.Fatalf("run=%+v", res)
	}
	var kinds []string
	for {
		select {
		case ev := <-sup.Events:
			kinds = append(kinds, ev.Ev.Kind)
			if ev.Task != "review" || ev.CallID != "call-a" || ev.GroupID != "group" || ev.GroupSize != 2 || ev.GroupPos != 1 {
				t.Fatalf("event=%+v", ev)
			}
		default:
			if strings.Join(kinds, ",") != "start,tool,done" {
				t.Fatalf("kinds=%v", kinds)
			}
			return
		}
	}
}

func TestInspectCallsOverlapInReadonlyParent(t *testing.T) {
	started := make(chan RunOptions, 2)
	release := make(chan struct{})
	runner := configuredScriptedRunner{run: func(ctx context.Context, _, input string, _ chan<- AgentEvent, opts RunOptions) (string, error) {
		started <- opts
		select {
		case <-release:
			return "report: " + input, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}
	sup := NewSupervisor(runner, testBudget())
	dir := t.TempDir()
	parentTool := AgentTool{Sup: sup, Dir: dir}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{
			Content: []llm.ContentBlock{
				{Type: "tool_use", ID: "a", Name: "agent", Input: map[string]any{"prompt": "inspect a", "mode": "inspect"}},
				{Type: "tool_use", ID: "b", Name: "agent", Input: map[string]any{"prompt": "inspect b", "mode": "inspect"}},
			},
			StopReason: "tool_use",
		},
		llmtest.Text("combined"),
	}}
	ag := agent.New(agent.Config{
		Model:            "m",
		Provider:         prov,
		Tools:            tools.NewRegistry(parentTool),
		Policy:           permission.New(string(permission.ModeReadonly), dir),
		MaxParallelTools: 2,
	})
	done := make(chan error, 1)
	go func() {
		_, err := ag.Send(context.Background(), "inspect both")
		done <- err
	}()

	opts := []RunOptions{<-started, <-started}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, opt := range opts {
		if opt.PermissionMode != permission.ModeReadonly || !slices.Equal(opt.Tools, inspectAgentTools) {
			t.Fatalf("inspect options=%+v", opt)
		}
	}
}

func TestAgentToolModesFailClosed(t *testing.T) {
	tool := AgentTool{}
	inspect := map[string]any{"prompt": "review", "mode": "inspect"}
	if !tool.ParallelSafe(inspect) || !tool.ReadOnly(inspect) {
		t.Fatal("valid inspect call should be parallel-safe and read-only")
	}
	for _, input := range []map[string]any{
		{"prompt": "review"},
		{"prompt": "review", "mode": "work"},
		{"prompt": "review", "mode": "unknown"},
		{"mode": "inspect"},
	} {
		if tool.ParallelSafe(input) || tool.ReadOnly(input) {
			t.Fatalf("call did not fail closed: %#v", input)
		}
	}
}

func TestAgentToolEnvelope(t *testing.T) {
	sup, dir := newTestSupervisor(t, func(_ context.Context, _ string, input string, _ chan<- AgentEvent) (string, error) {
		return "report: " + input, nil
	}, testBudget())
	tool := AgentTool{Sup: sup, Dir: dir}
	out, err := tool.Run(context.Background(), map[string]any{"prompt": "check this"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"ok":true`) || !strings.Contains(out, "report: check this") {
		t.Fatalf("out=%q", out)
	}
	if _, err := tool.Run(context.Background(), map[string]any{}); err == nil {
		t.Fatal("missing prompt should error")
	}
	if _, err := tool.Run(context.Background(), map[string]any{"prompt": "x", "mode": "unknown"}); err == nil {
		t.Fatal("invalid mode should error")
	}
}

func TestRunAgentPromptFailures(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, string, string, chan<- AgentEvent) (string, error)
		want string
	}{
		{name: "empty", run: func(context.Context, string, string, chan<- AgentEvent) (string, error) { return "", nil }, want: "empty result"},
		{name: "error", run: func(context.Context, string, string, chan<- AgentEvent) (string, error) {
			return "partial", errors.New("malformed")
		}, want: "malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sup, dir := newTestSupervisor(t, tt.run, testBudget())
			res := sup.RunAgentPrompt(context.Background(), dir, "review")
			if res.Ok || !strings.Contains(res.Output, tt.want) {
				t.Fatalf("result=%+v", res)
			}
		})
	}
}

func TestRunAgentPromptTimeoutPreservesPartialOutput(t *testing.T) {
	budget := testBudget()
	budget.MaxWall = 20 * time.Millisecond
	sup, dir := newTestSupervisor(t, func(ctx context.Context, _ string, _ string, _ chan<- AgentEvent) (string, error) {
		<-ctx.Done()
		return "partial findings", ctx.Err()
	}, budget)
	res := sup.RunAgentPrompt(context.Background(), dir, "review")
	if res.Ok || res.Code != "timeout" || !strings.Contains(res.Output, "partial findings") {
		t.Fatalf("result=%+v", res)
	}
}

func TestRunAgentPromptCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sup, dir := newTestSupervisor(t, func(ctx context.Context, _ string, _ string, _ chan<- AgentEvent) (string, error) {
		return "partial", ctx.Err()
	}, testBudget())
	res := sup.RunAgentPrompt(ctx, dir, "review")
	if res.Ok || res.Code != "canceled" || !strings.Contains(res.Output, "context canceled") {
		t.Fatalf("result=%+v", res)
	}
}

type temporaryError struct{ error }

func (temporaryError) Temporary() bool { return true }

func TestAgentToolRetriesOnlyTemporaryFailures(t *testing.T) {
	for _, tt := range []struct {
		name      string
		err       error
		wantCalls int32
	}{
		{name: "temporary", err: temporaryError{errors.New("retry")}, wantCalls: 3},
		{name: "permanent", err: errors.New("stop"), wantCalls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			sup, dir := newTestSupervisor(t, func(context.Context, string, string, chan<- AgentEvent) (string, error) {
				calls.Add(1)
				return "partial", tt.err
			}, testBudget())
			tool := AgentTool{Sup: sup, Dir: dir}
			if _, err := tool.Run(context.Background(), map[string]any{"prompt": "review", "max_retries": 2}); err != nil {
				t.Fatal(err)
			}
			if got := calls.Load(); got != tt.wantCalls {
				t.Fatalf("calls=%d want=%d", got, tt.wantCalls)
			}
		})
	}
}

func TestConcurrentInspectOptionsAreIndependent(t *testing.T) {
	var mu sync.Mutex
	var seen [][]string
	runner := configuredScriptedRunner{run: func(_ context.Context, _, _ string, _ chan<- AgentEvent, opts RunOptions) (string, error) {
		mu.Lock()
		seen = append(seen, append([]string(nil), opts.Tools...))
		mu.Unlock()
		opts.Tools[0] = "mutated"
		return "done", nil
	}}
	sup := NewSupervisor(runner, testBudget())
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := sup.RunAgentPrompt(context.Background(), t.TempDir(), "inspect", PromptOptions{Mode: AgentModeInspect})
			if !res.Ok {
				t.Errorf("result=%+v", res)
			}
		}()
	}
	wg.Wait()
	for _, tools := range seen {
		if !slices.Equal(tools, inspectAgentTools) {
			t.Fatalf("shared options mutated: %v", seen)
		}
	}
}

func TestRetryCountIsBounded(t *testing.T) {
	for _, tc := range []struct {
		input any
		want  int
	}{{-1, 0}, {2, 2}, {99, 5}, {float64(3), 3}, {"2", 0}} {
		if got := parseRetryCount(tc.input); got != tc.want {
			t.Errorf("parseRetryCount(%v)=%d want %d", tc.input, got, tc.want)
		}
	}
}

func TestRunnerErrorFormatting(t *testing.T) {
	sup, dir := newTestSupervisor(t, func(context.Context, string, string, chan<- AgentEvent) (string, error) {
		return "partial", fmt.Errorf("provider failed")
	}, testBudget())
	res := sup.RunAgentPrompt(context.Background(), dir, "review")
	if !strings.Contains(res.Output, "provider failed") || !strings.Contains(res.Output, "partial") {
		t.Fatalf("result=%+v", res)
	}
}
