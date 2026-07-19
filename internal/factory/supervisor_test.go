package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type scriptedRunner struct {
	run func(context.Context, string, string, chan<- AgentEvent) (string, error)
}

func (r scriptedRunner) RunAgent(ctx context.Context, dir, input string, events chan<- AgentEvent) (string, error) {
	return r.run(ctx, dir, input, events)
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

func TestEventLifecycle(t *testing.T) {
	sup, dir := newTestSupervisor(t, func(_ context.Context, _ string, _ string, events chan<- AgentEvent) (string, error) {
		events <- AgentEvent{Kind: "tool", Body: "read main.go"}
		return "done", nil
	}, testBudget())

	if res := sup.RunAgentPrompt(context.Background(), dir, "review"); !res.Ok {
		t.Fatalf("run=%+v", res)
	}
	var kinds []string
	for {
		select {
		case ev := <-sup.Events:
			kinds = append(kinds, ev.Ev.Kind)
			if ev.Task != "review" {
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
	if res.Ok || !strings.Contains(res.Output, "partial findings") || !strings.Contains(res.Output, "wall-clock limit") {
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
	if res.Ok || !strings.Contains(res.Output, "context canceled") {
		t.Fatalf("result=%+v", res)
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
