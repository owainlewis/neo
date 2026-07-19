package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/auth"
	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm/google"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/llm/openrouter"
	"github.com/owainlewis/neo/internal/session"
)

func TestModelChoices_OpenAISubscriptionOnlyListsSupportedCodexModel(t *testing.T) {
	clearAdditionalProviderCredentials(t)
	choices := modelChoices(context.Background(), &config.Config{
		Provider:   "openai",
		OpenAIAuth: config.OpenAIAuthSubscription,
	}, "openai")

	if len(choices) != 1 {
		t.Fatalf("subscription choices = %d, want 1: %#v", len(choices), choices)
	}
	if choices[0].ID != "gpt-5-codex" {
		t.Fatalf("subscription model = %q, want gpt-5-codex", choices[0].ID)
	}
}

func TestModelChoices_OpenAIAPIKeyDoesNotListCodexModels(t *testing.T) {
	clearAdditionalProviderCredentials(t)
	choices := modelChoices(context.Background(), &config.Config{
		Provider:   "openai",
		OpenAIAuth: config.OpenAIAuthAPIKey,
	}, "openai")

	for _, choice := range choices {
		if strings.Contains(choice.ID, "codex") {
			t.Fatalf("api-key model picker should not list Codex model %q", choice.ID)
		}
	}
}

func TestModelChoices_GoogleListsGeminiModels(t *testing.T) {
	clearAdditionalProviderCredentials(t)
	choices := modelChoices(context.Background(), &config.Config{Provider: "google"}, "google")
	if len(choices) == 0 {
		t.Fatal("expected google model choices")
	}
	if choices[0].ID != google.DefaultModel {
		t.Fatalf("first google choice = %q, want default %q", choices[0].ID, google.DefaultModel)
	}
	foundFlash := false
	for _, choice := range choices {
		if strings.HasPrefix(choice.ID, "gemini-") && strings.Contains(choice.ID, "flash") {
			foundFlash = true
		}
	}
	if !foundFlash {
		t.Fatalf("expected at least one Gemini Flash choice: %#v", choices)
	}
}

func TestUsageDocumentsGoogleProvider(t *testing.T) {
	for _, want := range []string{`"google"`, "GOOGLE_API_KEY"} {
		if !strings.Contains(usageText, want) {
			t.Fatalf("usage does not contain %q", want)
		}
	}
}

func TestModelChoices_OpenRouterFallsBackWhenCatalogueUnavailable(t *testing.T) {
	clearAdditionalProviderCredentials(t)
	// Point the picker at an unroutable network so the live fetch fails fast;
	// the picker must still return the provider default rather than nothing.
	t.Setenv("OPENROUTER_API_KEY", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already-cancelled context forces the fetch to fail immediately

	choices := modelChoices(ctx, &config.Config{Provider: "openrouter"}, "openrouter")
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

func TestModelChoices_ListsModelsAcrossCredentialedProviders(t *testing.T) {
	clearAdditionalProviderCredentials(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")

	choices := modelChoices(context.Background(), &config.Config{Provider: "anthropic", Model: "claude-opus-4-8"}, "anthropic")
	providers := map[string]bool{}
	for _, choice := range choices {
		providers[choice.Provider] = true
	}
	if !providers["anthropic"] || !providers["openai"] {
		t.Fatalf("providers = %v, want anthropic and openai", providers)
	}
	if providers["google"] || providers["openrouter"] {
		t.Fatalf("picker exposed providers without credentials: %v", providers)
	}
}

func TestChatSessionProvider_RejectsExpiredSubscriptionCredentialOnResume(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := auth.NewStore(filepath.Join(home, ".neo", "auth.json"))
	if err := store.Set(auth.ProviderOpenAICodex, auth.Credentials{
		AccessToken: "expired",
		ExpiresAt:   time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	_, err := chatSessionProvider(context.Background(), &config.Config{OpenAIAuth: config.OpenAIAuthSubscription}, &session.Session{}, "openai")
	if err == nil || !strings.Contains(err.Error(), "session expired") {
		t.Fatalf("checkedProvider error = %v, want expired credential error", err)
	}
}

func clearAdditionalProviderCredentials(t *testing.T) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("HOME", t.TempDir())
}

func TestDoctorCredentialCheckFailsWhenEnvCredentialMissing(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	got := doctorCredentialCheck(&config.Config{Provider: "anthropic"})
	if got.Status != doctorFail {
		t.Fatalf("status = %s, want fail", got.Status)
	}
	if !strings.Contains(got.Detail, "ANTHROPIC_API_KEY") {
		t.Fatalf("detail should name missing env var, got %q", got.Detail)
	}
}

func TestDoctorCredentialCheckDoesNotPrintSecretValue(t *testing.T) {
	const secret = "sk-test-secret"
	t.Setenv("OPENAI_API_KEY", secret)
	got := doctorCredentialCheck(&config.Config{Provider: "openai", OpenAIAuth: config.OpenAIAuthAPIKey})
	if got.Status != doctorPass {
		t.Fatalf("status = %s, want pass (%s)", got.Status, got.Detail)
	}
	if strings.Contains(got.Detail, secret) {
		t.Fatalf("doctor detail exposed secret: %q", got.Detail)
	}
	if !strings.Contains(got.Detail, "OPENAI_API_KEY") {
		t.Fatalf("detail should name credential source, got %q", got.Detail)
	}
}

func TestDoctorProviderCheckRejectsUnknownProvider(t *testing.T) {
	got := doctorProviderCheck(&config.Config{Provider: "wat"})
	if got.Status != doctorFail {
		t.Fatalf("status = %s, want fail", got.Status)
	}
}

func TestDoctorChecksContinueAfterConfigFailure(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "neo.yaml"), []byte("permissions: [invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	checks := doctorChecks()
	want := []string{"config", "provider", "credentials", "model", "sessions", "git", "workspace"}
	if len(checks) != len(want) {
		t.Fatalf("checks = %d, want %d: %#v", len(checks), len(want), checks)
	}
	for i, name := range want {
		if checks[i].Name != name {
			t.Fatalf("check %d = %s, want %s", i, checks[i].Name, name)
		}
	}
	if checks[0].Status != doctorFail {
		t.Fatalf("config status = %s, want fail", checks[0].Status)
	}
	for _, check := range checks[1:4] {
		if check.Status != doctorWarn || !strings.Contains(check.Detail, "skipped: config failed to load") {
			t.Fatalf("%s check = %s/%q, want skipped warning", check.Name, check.Status, check.Detail)
		}
	}
}

func TestChatSystem_IgnoresProjectMemoryFile(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "pkg")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory.md"), []byte("must not enter the prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	system, blocks := chatSystem(&config.Config{}, cwd, nil)

	if len(blocks) != 1 {
		t.Fatalf("system blocks = %d, want 1", len(blocks))
	}
	if strings.Contains(system, "must not enter the prompt") {
		t.Fatal("memory.md should not enter the system prompt")
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

func TestSessionBackend_HonorsSavedModelForSameProvider(t *testing.T) {
	cfg := &config.Config{Provider: "openai", Model: "gpt-5.2"}
	meta := session.Metadata{Provider: "openai", Model: "gpt-5-mini"}
	provider, model := sessionBackend(cfg, meta)
	if provider != "openai" || model != "gpt-5-mini" {
		t.Fatalf("session backend = %s/%s, want openai/gpt-5-mini", provider, model)
	}
}

func TestSessionBackend_FallsBackWhenSavedProviderCredentialsAreMissing(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cfg := &config.Config{Provider: "anthropic", Model: "claude-opus-4-8"}
	meta := session.Metadata{Provider: "openai", Model: "gpt-5-codex"}
	provider, model := sessionBackend(cfg, meta)
	if provider != "anthropic" || model != "claude-opus-4-8" {
		t.Fatalf("session backend = %s/%s, want anthropic/claude-opus-4-8", provider, model)
	}
}

func TestSessionBackend_RestoresSavedProviderWhenCredentialIsConfigured(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	cfg := &config.Config{Provider: "anthropic", Model: "claude-opus-4-8"}
	meta := session.Metadata{Provider: "openai", Model: "gpt-5.2"}
	provider, model := sessionBackend(cfg, meta)
	if provider != "openai" || model != "gpt-5.2" {
		t.Fatalf("session backend = %s/%s, want openai/gpt-5.2", provider, model)
	}
}

func TestSessionBackend_FallsBackForLegacySessionsWithoutProvider(t *testing.T) {
	// Sessions written before the provider field existed must not pin a model
	// that may belong to a different backend.
	cfg := &config.Config{Provider: "anthropic", Model: "claude-opus-4-8"}
	meta := session.Metadata{Model: "gpt-4o"}
	provider, model := sessionBackend(cfg, meta)
	if provider != "anthropic" || model != "claude-opus-4-8" {
		t.Fatalf("session backend = %s/%s, want anthropic/claude-opus-4-8", provider, model)
	}
}

func TestChatCompactorUsesContextWindowOverride(t *testing.T) {
	got := chatCompactor(&llmtest.FakeProvider{}, "m", &config.Config{
		Compaction: config.Compaction{ContextWindowTokens: 1_000_000},
	})
	s, ok := got.(compact.Summarizer)
	if !ok {
		t.Fatalf("compactor = %T, want compact.Summarizer", got)
	}
	if s.TriggerTokens != 700_000 {
		t.Fatalf("trigger tokens = %d, want 700000", s.TriggerTokens)
	}
}

func TestPrintSessionSearchResultsIncludesMetadataAndExcerpt(t *testing.T) {
	var out bytes.Buffer
	printSessionSearchResults(&out, []session.SearchResult{{
		Metadata: session.Metadata{
			ID:        "sess_1",
			UpdatedAt: time.Date(2026, 7, 5, 12, 0, 0, 0, time.Local),
			Model:     "test-model",
			CWD:       "/repo",
			Title:     "Bug fix",
		},
		Excerpt: "fixed the token bug",
	}})
	text := out.String()
	for _, want := range []string{"ID", "UPDATED", "MODEL", "CWD", "TITLE", "MATCH", "sess_1", "test-model", "/repo", "Bug fix", "fixed the token bug"} {
		if !strings.Contains(text, want) {
			t.Fatalf("search output missing %q:\n%s", want, text)
		}
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
