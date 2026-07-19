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

func TestRegistryHasCodingToolsWithoutNestedAgent(t *testing.T) {
	r := &AgentRunner{Root: t.TempDir()}
	names := r.registry(t.TempDir()).Names()
	for _, want := range []string{"bash", "read_file", "write_file", "edit_file", "grep", "glob"} {
		if !slices.Contains(names, want) {
			t.Fatalf("registry %v missing %s", names, want)
		}
	}
	if slices.Contains(names, "agent") {
		t.Fatalf("subagents must not receive nested delegation: %v", names)
	}
}

func TestRunAgentReportsUsageAndUsesFixedPrompt(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{{
		Content:    []llm.ContentBlock{{Type: "text", Text: "VERDICT: PASS"}},
		StopReason: "end_turn",
		Usage:      llm.Usage{InputTokens: 1_000_000, OutputTokens: 100_000},
	}}}
	r := &AgentRunner{Provider: prov, DefaultModel: "model", Root: t.TempDir()}
	events := make(chan AgentEvent, 16)
	out, err := r.RunAgent(context.Background(), t.TempDir(), "PR #1", events)
	if err != nil {
		t.Fatal(err)
	}
	if out != "VERDICT: PASS" || prov.Calls[0].System != dynamicAgentSystemPrompt || prov.Calls[0].Model != "model" {
		t.Fatalf("out=%q request=%+v", out, prov.Calls[0])
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
	if !slices.Contains(kinds, "text") || usage != "tokens in=1000000 out=100000 cached=0" {
		t.Fatalf("kinds=%v usage=%q", kinds, usage)
	}
}

func TestAgentRunnerSetBackendAppliesToFutureWorkers(t *testing.T) {
	oldProvider := &llmtest.FakeProvider{}
	newProvider := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("new backend")}}
	r := &AgentRunner{Provider: oldProvider, DefaultModel: "old-model", Root: t.TempDir()}
	if err := r.SetBackend(newProvider, "new-model"); err != nil {
		t.Fatal(err)
	}
	out, err := r.RunAgent(context.Background(), t.TempDir(), "task", make(chan AgentEvent, 16))
	if err != nil {
		t.Fatal(err)
	}
	if out != "new backend" || len(oldProvider.Calls) != 0 || newProvider.Calls[0].Model != "new-model" {
		t.Fatalf("out=%q old=%d new=%+v", out, len(oldProvider.Calls), newProvider.Calls)
	}
}

func TestReadonlyModePropagatesToSubagents(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("t1", "write_file", map[string]any{"path": "x.txt", "content": "hi"}),
		llmtest.Text("done"),
	}}
	dir := t.TempDir()
	r := &AgentRunner{Provider: prov, DefaultModel: "m", Root: dir, Mode: permission.ModeReadonly}
	if _, err := r.RunAgent(context.Background(), dir, "write x.txt", make(chan AgentEvent, 32)); err != nil {
		t.Fatal(err)
	}
	flat := ""
	for _, m := range prov.Calls[1].Messages {
		for _, c := range m.Content {
			flat += c.Content
		}
	}
	if !strings.Contains(flat, "readonly") {
		t.Fatalf("write_file not denied:\n%s", flat)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.txt")); err == nil {
		t.Fatal("file was written despite readonly mode")
	}
}

func TestTranslateStatusLines(t *testing.T) {
	ev, ok := translate(agent.Event{Kind: agent.EventToolCall, Name: "bash", Args: map[string]any{"command": "just test"}})
	if !ok || ev.Body != "$ just test" {
		t.Fatalf("bash status=%+v", ev)
	}
	if _, ok := translate(agent.Event{Kind: agent.EventToolCall, Name: "agent"}); ok {
		t.Fatal("agent call should not produce a status event")
	}
}
