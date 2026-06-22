package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm/openrouter"
	"github.com/owainlewis/neo/internal/session"
)

func TestModelChoices_OpenAISubscriptionOnlyListsSupportedCodexModel(t *testing.T) {
	choices := modelChoices(context.Background(), &config.Config{
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
	choices := modelChoices(context.Background(), &config.Config{
		Provider:   "openai",
		OpenAIAuth: config.OpenAIAuthAPIKey,
	})

	for _, choice := range choices {
		if strings.Contains(choice.ID, "codex") {
			t.Fatalf("api-key model picker should not list Codex model %q", choice.ID)
		}
	}
}

func TestModelChoices_OpenRouterFallsBackWhenCatalogueUnavailable(t *testing.T) {
	// Point the picker at an unroutable network so the live fetch fails fast;
	// the picker must still return the provider default rather than nothing.
	t.Setenv("OPENROUTER_API_KEY", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already-cancelled context forces the fetch to fail immediately

	choices := modelChoices(ctx, &config.Config{Provider: "openrouter"})
	if len(choices) == 0 {
		t.Fatal("expected a fallback openrouter model choice")
	}
	found := false
	for _, choice := range choices {
		if choice.ID == openrouter.DefaultModel {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("fallback choices missing default %q: %#v", openrouter.DefaultModel, choices)
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

func TestChatSystem_IncludesGitContextAsDistinctDynamicBlock(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.name", "Neo Test")
	runGit(t, root, "config", "user.email", "neo@example.com")
	cwd := filepath.Join(root, "pkg")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "pkg/tracked.txt")
	runGit(t, root, "commit", "-m", "seed commit")
	if err := os.WriteFile(filepath.Join(cwd, "tracked.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	system, blocks := chatSystem(&config.Config{}, cwd, nil)

	if len(blocks) != 2 {
		t.Fatalf("system blocks = %d, want 2", len(blocks))
	}
	if blocks[1].Cache {
		t.Fatal("expected git block to stay dynamic")
	}
	for _, want := range []string{"# Git context", "Branch: main", "M tracked.txt", "seed commit"} {
		if !strings.Contains(blocks[1].Text, want) {
			t.Fatalf("git block missing %q\n---\n%s", want, blocks[1].Text)
		}
	}
	if !strings.Contains(system, blocks[1].Text) {
		t.Fatal("flattened system prompt missing git block")
	}
}

func TestChatSystem_SkipsGitContextOutsideRepo(t *testing.T) {
	cwd := t.TempDir()

	system, blocks := chatSystem(&config.Config{}, cwd, nil)

	if len(blocks) != 1 {
		t.Fatalf("system blocks = %d, want 1", len(blocks))
	}
	if strings.Contains(system, "# Git context") {
		t.Fatal("git context should be skipped outside a repo")
	}
}

func TestSessionModel_HonorsSavedModelForSameProvider(t *testing.T) {
	cfg := &config.Config{Provider: "openai", Model: "gpt-5.2"}
	meta := session.Metadata{Provider: "openai", Model: "gpt-5-mini"}
	if got := sessionModel(cfg, meta); got != "gpt-5-mini" {
		t.Fatalf("sessionModel = %q, want saved model gpt-5-mini", got)
	}
}

func TestSessionModel_FallsBackOnProviderMismatch(t *testing.T) {
	cfg := &config.Config{Provider: "anthropic", Model: "claude-opus-4-8"}
	meta := session.Metadata{Provider: "openai", Model: "gpt-5-codex"}
	if got := sessionModel(cfg, meta); got != "claude-opus-4-8" {
		t.Fatalf("sessionModel = %q, want config model on provider switch", got)
	}
}

func TestSessionModel_FallsBackForLegacySessionsWithoutProvider(t *testing.T) {
	// Sessions written before the provider field existed must not pin a model
	// that may belong to a different backend.
	cfg := &config.Config{Provider: "anthropic", Model: "claude-opus-4-8"}
	meta := session.Metadata{Model: "gpt-4o"}
	if got := sessionModel(cfg, meta); got != "claude-opus-4-8" {
		t.Fatalf("sessionModel = %q, want config model for legacy session", got)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
