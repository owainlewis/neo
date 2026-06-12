package factory

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/permission"
)

func TestRegistryIsTheRole(t *testing.T) {
	r := &AgentRunner{Root: t.TempDir()}

	verifyLike := Step{Name: "verify", Kind: "agent", Tools: []string{"bash", "read_file"}}
	names := r.registry(verifyLike, t.TempDir(), 1).Names()
	if !slices.Equal(names, []string{"bash", "read_file"}) {
		t.Fatalf("verify registry = %v; the role must be its tool set", names)
	}

	workerLike := Step{Name: "worker", Kind: "agent",
		Tools: []string{"bash", "read_file", "write_file", "edit_file", "run_step"}}
	names = r.registry(workerLike, t.TempDir(), 1).Names()
	if !slices.Contains(names, "run_step") || !slices.Contains(names, "write_file") {
		t.Fatalf("worker registry = %v", names)
	}

	// No frontmatter tools = observation only, never run_step.
	bare := Step{Name: "bare", Kind: "agent"}
	names = r.registry(bare, t.TempDir(), 1).Names()
	if slices.Contains(names, "write_file") || slices.Contains(names, "run_step") {
		t.Fatalf("default registry too permissive: %v", names)
	}
}

func TestRunAgentStepReportsUsage(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{{
		Content:    []llm.ContentBlock{{Type: "text", Text: "VERDICT: PASS"}},
		StopReason: "end_turn",
		Usage:      llm.Usage{InputTokens: 1_000_000, OutputTokens: 100_000},
	}}}
	r := &AgentRunner{Provider: prov, DefaultModel: "claude-sonnet-4-6", Root: t.TempDir()}

	events := make(chan AgentEvent, 16)
	out, err := r.RunAgentStep(context.Background(), Step{Name: "verify", Kind: "agent", Prompt: "Verify."},
		t.TempDir(), "PR #1", 1, events)
	if err != nil {
		t.Fatal(err)
	}
	if out != "VERDICT: PASS" {
		t.Fatalf("out = %q", out)
	}
	close(events)

	var kinds []string
	var usage string
	for ev := range events {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == "usage" {
			usage = ev.Body
		}
	}
	if !slices.Contains(kinds, "text") || !slices.Contains(kinds, "usage") {
		t.Fatalf("event kinds = %v", kinds)
	}
	if usage != "tokens in=1000000 out=100000 cached=0" {
		t.Fatalf("usage body = %q", usage)
	}
}

// TestReadonlyModePropagatesToSteps guards the permission boundary: a
// readonly session delegating a step must not gain write access through
// the child agent.
func TestReadonlyModePropagatesToSteps(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("t1", "write_file", map[string]any{"path": "x.txt", "content": "hi"}),
		llmtest.Text("done"),
	}}
	dir := t.TempDir()
	r := &AgentRunner{Provider: prov, DefaultModel: "m", Root: dir, Mode: permission.ModeReadonly}

	events := make(chan AgentEvent, 32)
	step := Step{Name: "w", Kind: "agent", Prompt: "Write.",
		Tools: []string{"bash", "read_file", "write_file"}}
	if _, err := r.RunAgentStep(context.Background(), step, dir, "write x.txt", 1, events); err != nil {
		t.Fatal(err)
	}
	// The write must be denied by policy: the tool_result fed back to the
	// model carries the denial.
	res := prov.Calls[1].Messages
	flat := ""
	for _, m := range res {
		for _, c := range m.Content {
			flat += c.Content
		}
	}
	if !strings.Contains(flat, "readonly") {
		t.Fatalf("write_file not denied under readonly mode:\n%s", flat)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.txt")); err == nil {
		t.Fatal("file was written despite readonly mode")
	}
}

func TestTranslateStatusLines(t *testing.T) {
	ev, ok := translate(agent.Event{Kind: agent.EventToolCall, Name: "bash",
		Args: map[string]any{"command": "just test"}})
	if !ok || ev.Body != "$ just test" {
		t.Fatalf("bash status = %+v", ev)
	}
	// run_step calls are dropped: the child's own start event renders it.
	if _, ok := translate(agent.Event{Kind: agent.EventToolCall, Name: "run_step",
		Args: map[string]any{"name": "verify"}}); ok {
		t.Fatal("run_step call should not produce a status event")
	}
	ev, ok = translate(agent.Event{Kind: agent.EventToolCall, Name: "read_file",
		Args: map[string]any{"path": "main.go"}})
	if !ok || ev.Body != "read main.go" {
		t.Fatalf("read_file status = %+v", ev)
	}
}

func TestRunAgentStepSystemPromptIsTheStepBody(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok")}}
	r := &AgentRunner{Provider: prov, DefaultModel: "m", Root: t.TempDir()}

	events := make(chan AgentEvent, 16)
	_, err := r.RunAgentStep(context.Background(),
		Step{Name: "triage", Kind: "agent", Prompt: "You are the TRIAGE step.", Model: "pinned-model"},
		t.TempDir(), "report", 1, events)
	if err != nil {
		t.Fatal(err)
	}
	req := prov.Calls[0]
	if req.System != "You are the TRIAGE step." {
		t.Fatalf("system = %q", req.System)
	}
	if req.Model != "pinned-model" {
		t.Fatalf("model = %q; frontmatter pin must win", req.Model)
	}
}
