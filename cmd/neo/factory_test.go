package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
)

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
