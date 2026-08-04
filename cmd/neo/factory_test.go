package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
)

func TestSubagentBackendFollowsCoordinatorByDefault(t *testing.T) {
	fallback := &llmtest.FakeProvider{}
	prov, model, follows := subagentBackend(context.Background(), &config.Config{}, fallback, "main-model")
	if prov != fallback || model != "main-model" || !follows {
		t.Fatalf("backend = %T/%q follows=%t", prov, model, follows)
	}
}

func TestSubagentBackendUsesConfiguredProviderAndModel(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	cfg := &config.Config{Subagents: config.Backend{Provider: "anthropic", Model: "worker-model"}}
	prov, model, follows := subagentBackend(context.Background(), cfg, &llmtest.FakeProvider{}, "main-model")
	if prov.Name() != "anthropic" || model != "worker-model" || follows {
		t.Fatalf("backend = %s/%q follows=%t", prov.Name(), model, follows)
	}
}

func TestSubagentBackendCredentialFailureDoesNotBreakCoordinator(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	fallback := &llmtest.FakeProvider{}
	cfg := &config.Config{Subagents: config.Backend{Provider: "google", Model: "worker-model"}}
	prov, model, follows := subagentBackend(context.Background(), cfg, fallback, "main-model")
	if prov == fallback || model != "worker-model" || follows {
		t.Fatalf("backend = %T/%q follows=%t", prov, model, follows)
	}
	_, err := prov.Complete(context.Background(), llm.Request{Model: model})
	if err == nil {
		t.Fatal("expected unavailable worker backend to fail")
	}
	for _, want := range []string{"subagent backend", "google/worker-model", "GOOGLE_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestChatSystemAdvertisesAgentToolWorkflowPattern(t *testing.T) {
	system, blocks := chatSystem(&config.Config{}, t.TempDir(), nil, io.Discard)
	for _, want := range []string{
		"user's request",
		"AGENTS.md",
		"an invoked skill",
		"your own plan",
		"always render them through the",
		"Do not invent a workflow for a simple single-step request",
		"agent tool",
		"subagent prompts",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q workflow guidance:\n%s", want, system)
		}
	}
	if len(blocks) == 0 || !blocks[0].Cache {
		t.Fatalf("base prompt should be cacheable: %+v", blocks)
	}
}

func TestChatSystemAdvertisesNamedPhasesWithoutPromptBodies(t *testing.T) {
	system, _ := chatSystem(&config.Config{}, t.TempDir(), nil, io.Discard)
	for _, want := range []string{"# Named phases", "/design", "/plan", "/build", "/review"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q named phase catalog:\n%s", want, system)
		}
	}
	if strings.Contains(system, "Design the requested change before implementation.") {
		t.Fatalf("system prompt included named phase body:\n%s", system)
	}
}

func TestChatSystemPreservesAgentsWorkflowInstructions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	const instructions = "Follow this workflow when changing code:\n1. Inspect the issue\n2. Implement the change\n3. Launch a review subagent"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(instructions), 0o644); err != nil {
		t.Fatal(err)
	}

	system, blocks := chatSystem(&config.Config{}, root, nil, io.Discard)

	if len(blocks) < 2 {
		t.Fatalf("system blocks = %d, want project instructions block", len(blocks))
	}
	if !strings.Contains(system, instructions) {
		t.Fatalf("AGENTS.md workflow was not preserved:\n%s", system)
	}
}

func TestChatSystemWarnsAndExcludesEscapingAgentsSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	cwd := filepath.Join(root, "pkg")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	t.Setenv("HOME", home)
	const global = "safe global instructions remain loaded"
	if err := os.MkdirAll(filepath.Join(home, ".neo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".neo", "AGENTS.md"), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	const local = "safe local instructions remain loaded"
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	const sentinel = "outside sentinel must never enter the system prompt"
	outside := filepath.Join(base, "outside-AGENTS.md")
	if err := os.WriteFile(outside, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	var warnings bytes.Buffer
	system, blocks := chatSystem(&config.Config{}, cwd, nil, &warnings)

	if strings.Contains(system, sentinel) {
		t.Fatal("escaping AGENTS.md target entered the system prompt")
	}
	for _, safe := range []string{global, local} {
		if !strings.Contains(system, safe) {
			t.Fatalf("system prompt did not preserve %q", safe)
		}
	}
	if len(blocks) != 2 {
		t.Fatalf("system blocks = %d, want base and safe instructions blocks", len(blocks))
	}
	for _, want := range []string{"warning: AGENTS.md:", "must resolve within workspace root"} {
		if !strings.Contains(warnings.String(), want) {
			t.Fatalf("warning %q does not contain %q", warnings.String(), want)
		}
	}
}
