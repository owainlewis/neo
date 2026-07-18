package main

import (
	"context"
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
	system, blocks := chatSystem(&config.Config{}, t.TempDir(), nil)
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

	system, blocks := chatSystem(&config.Config{}, root, nil)

	if len(blocks) < 2 {
		t.Fatalf("system blocks = %d, want project instructions block", len(blocks))
	}
	if !strings.Contains(system, instructions) {
		t.Fatalf("AGENTS.md workflow was not preserved:\n%s", system)
	}
}
