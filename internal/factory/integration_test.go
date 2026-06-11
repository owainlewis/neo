package factory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
)

// TestFullFactoryFlow walks the whole machine with a scripted provider and
// no network: an orchestrator delegates a ticket to a worker; the worker
// runs a script step (checks) and an agent step (verify), then reports; the
// orchestrator summarizes. Execution is synchronous, so provider calls
// interleave deterministically:
//
//  1. orchestrator → run_step("worker", …)
//  2. worker       → run_step("checks", …)   (script: no LLM call)
//  3. worker       → run_step("verify", …)
//  4. verify       → "VERDICT: PASS …"
//  5. worker       → "OUTCOME: success …"
//  6. orchestrator → "retired #12 …"
func TestFullFactoryFlow(t *testing.T) {
	dir := t.TempDir()
	stepsDir := filepath.Join(dir, "steps")
	writeFile(t, filepath.Join(stepsDir, "orchestrator.md"), `---
tools: [run_step]
---
Orchestrate.`, 0o644)
	writeFile(t, filepath.Join(stepsDir, "worker.md"), `---
tools: [run_step]
---
Work.`, 0o644)
	writeFile(t, filepath.Join(stepsDir, "verify.md"), `---
tools: [read_file]
---
Verify.`, 0o644)
	writeFile(t, filepath.Join(stepsDir, "checks"),
		"#!/bin/sh\nread -r pr\necho \"ALL CHECKS GREEN for PR $pr\"\n", 0o755)

	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("t1", "run_step", map[string]any{
			"name": "worker", "input": "issue #12: invite teammate; acceptance: …"}),
		llmtest.ToolUse("t2", "run_step", map[string]any{
			"name": "checks", "input": "34"}),
		llmtest.ToolUse("t3", "run_step", map[string]any{
			"name": "verify", "input": "PR #34, branch feat/issue-12, acceptance: …"}),
		llmtest.Text("VERDICT: PASS\nEVIDENCE: suite green\nFINDINGS:"),
		llmtest.Text("OUTCOME: success\nPR: #34\nEVIDENCE: verifier PASS"),
		llmtest.Text("retired #12 via PR #34"),
	}}

	runner := &AgentRunner{Provider: prov, DefaultModel: "claude-sonnet-4-6", Root: dir}
	sup := NewSupervisor(runner, testBudget(), Resolver{Paths: []string{stepsDir}})
	runner.Sup = sup

	out, err := sup.Run(context.Background(), dir, "orchestrator", "work the backlog")
	if err != nil {
		t.Fatal(err)
	}
	if out != "retired #12 via PR #34" {
		t.Fatalf("final output = %q", out)
	}
	if got := len(prov.Calls); got != 6 {
		t.Fatalf("provider calls = %d, want 6", got)
	}

	// Tool results must carry child output back up: the worker's third call
	// happens after checks ran, so its transcript holds the script output.
	flat := transcriptText(prov.Calls[2].Messages)
	if !strings.Contains(flat, "ALL CHECKS GREEN for PR 34") {
		t.Errorf("script output not returned to worker:\n%s", flat)
	}
	// And the worker's report must reach the orchestrator.
	flat = transcriptText(prov.Calls[5].Messages)
	if !strings.Contains(flat, "OUTCOME: success") {
		t.Errorf("worker report not returned to orchestrator:\n%s", flat)
	}

	// The tree: orchestrator(d0) → worker(d1) → {checks, verify}(d2).
	byStep := map[string]NodeView{}
	for _, v := range sup.Snapshot() {
		byStep[v.Step] = v
	}
	if len(byStep) != 4 {
		t.Fatalf("nodes = %d, want 4", len(byStep))
	}
	orch, worker := byStep["orchestrator"], byStep["worker"]
	checks, verify := byStep["checks"], byStep["verify"]
	if worker.Parent != orch.ID || checks.Parent != worker.ID || verify.Parent != worker.ID {
		t.Errorf("wrong tree shape: %+v", byStep)
	}
	if orch.Depth != 0 || worker.Depth != 1 || checks.Depth != 2 || verify.Depth != 2 {
		t.Errorf("wrong depths: %+v", byStep)
	}
	if checks.Kind != "script" || verify.Kind != "agent" {
		t.Errorf("wrong kinds: checks=%s verify=%s", checks.Kind, verify.Kind)
	}
	for step, v := range byStep {
		if !v.Done || v.Err != "" {
			t.Errorf("%s not cleanly done: %+v", step, v)
		}
	}

	// The whole run is renderable.
	frame := RenderTree(sup.Snapshot())
	for _, want := range []string{"✓ orchestrator", "✓ worker", "✓ checks", "✓ verify"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame missing %q:\n%s", want, frame)
		}
	}
}

func transcriptText(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		for _, c := range m.Content {
			b.WriteString(c.Text)
			b.WriteString(c.Content)
			b.WriteString("\n")
		}
	}
	return b.String()
}
