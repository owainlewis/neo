package factory

import (
	"context"
	"strings"
	"testing"
)

func TestDynamicAgentPromptFlow(t *testing.T) {
	sup, dir := newTestSupervisor(t, scriptedAgent{run: func(_ context.Context, step Step, _ string, input string, _ int, events chan<- AgentEvent) (string, error) {
		if step.Name != "agent" {
			return "", nil
		}
		events <- AgentEvent{Kind: "text", Body: "working"}
		return "report: " + input, nil
	}}, testBudget(), nil)

	res := sup.RunAgentPrompt(context.Background(), 0, dir, "review the diff")
	if !res.Ok || res.Kind != "agent" || !strings.Contains(res.Output, "review the diff") {
		t.Fatalf("agent prompt result: %+v", res)
	}

	byStep := map[string]NodeView{}
	for _, v := range sup.Snapshot() {
		byStep[v.Step] = v
	}
	ag := byStep["agent"]
	if ag.Step != "agent" || ag.Kind != "agent" || ag.Parent != 0 || ag.Depth != 0 || !ag.Done {
		t.Fatalf("dynamic agent node = %+v", ag)
	}
	if frame := RenderTree(sup.Snapshot()); !strings.Contains(frame, "✓ agent") {
		t.Fatalf("rendered tree missing agent node:\n%s", frame)
	}
}
