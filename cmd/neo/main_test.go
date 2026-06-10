package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
)

func TestModelChoices_OpenAISubscriptionOnlyListsSupportedCodexModel(t *testing.T) {
	choices := modelChoices(&config.Config{
		Provider:   "openai",
		OpenAIAuth: config.OpenAIAuthSubscription,
	})

	if len(choices) != 1 {
		t.Fatalf("subscription choices = %d, want 1: %#v", len(choices), choices)
	}
	if choices[0].ID != "gpt-5-codex" {
		t.Fatalf("subscription model = %q, want gpt-5-codex", choices[0].ID)
	}
}

func TestModelChoices_OpenAIAPIKeyDoesNotListCodexModels(t *testing.T) {
	choices := modelChoices(&config.Config{
		Provider:   "openai",
		OpenAIAuth: config.OpenAIAuthAPIKey,
	})

	for _, choice := range choices {
		if strings.Contains(choice.ID, "codex") {
			t.Fatalf("api-key model picker should not list Codex model %q", choice.ID)
		}
	}
}

func TestChatSystem_IncludesProjectMemoryAsDistinctDynamicBlock(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "pkg")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory.md"), []byte("# Project memory\n\n- prefers small diffs\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	system, blocks := chatSystem(&config.Config{}, cwd, nil)

	if len(blocks) != 2 {
		t.Fatalf("system blocks = %d, want 2", len(blocks))
	}
	if blocks[0].Cache != true {
		t.Fatal("expected base block to stay cacheable by default")
	}
	if blocks[1].Cache {
		t.Fatal("expected memory block to stay dynamic")
	}
	if !strings.Contains(blocks[1].Text, "# Project memory") || !strings.Contains(blocks[1].Text, "prefers small diffs") {
		t.Fatalf("unexpected memory block: %q", blocks[1].Text)
	}
	if !strings.Contains(system, blocks[1].Text) {
		t.Fatal("flattened system prompt missing memory block")
	}
}

func TestChatSystem_SkipsProjectMemoryWhenDisabled(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "pkg")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory.md"), []byte("- hidden memory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	disabled := false

	system, blocks := chatSystem(&config.Config{Features: config.Features{Memory: &disabled}}, cwd, nil)

	if len(blocks) != 1 {
		t.Fatalf("system blocks = %d, want 1", len(blocks))
	}
	if strings.Contains(system, "hidden memory") {
		t.Fatal("disabled memory should not enter the prompt")
	}
}
