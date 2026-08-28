package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/auth"
	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/google"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/llm/openai"
	"github.com/owainlewis/neo/internal/llm/openrouter"
	"github.com/owainlewis/neo/internal/profile"
	"github.com/owainlewis/neo/internal/session"
)

func TestModelChoices_OpenAISubscriptionOnlyListsSupportedCodexModel(t *testing.T) {
	clearAdditionalProviderCredentials(t)
	choices := modelChoices(context.Background(), &config.Config{
		Provider:   "openai",
		OpenAIAuth: config.OpenAIAuthSubscription,
	}, "openai", io.Discard)

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
	}, "openai", io.Discard)

	for _, choice := range choices {
		if strings.Contains(choice.ID, "codex") {
			t.Fatalf("api-key model picker should not list Codex model %q", choice.ID)
		}
	}
}

func TestModelChoices_GoogleListsGeminiModels(t *testing.T) {
	clearAdditionalProviderCredentials(t)
	choices := modelChoices(context.Background(), &config.Config{Provider: "google"}, "google", io.Discard)
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

func TestPrintVersion(t *testing.T) {
	oldVersion := Version
	Version = "v1.2.3-test"
	t.Cleanup(func() { Version = oldVersion })

	var out bytes.Buffer
	printVersion(&out)

	if got, want := out.String(), "neo version v1.2.3-test\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestUsageDocumentsVersionAliases(t *testing.T) {
	for _, want := range []string{"neo version", "-v", "--version"} {
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

	choices := modelChoices(ctx, &config.Config{Provider: "openrouter"}, "openrouter", io.Discard)
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

func TestModelChoices_OnlyListsActiveProvider(t *testing.T) {
	clearAdditionalProviderCredentials(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")

	choices := modelChoices(context.Background(), &config.Config{Provider: "anthropic", Model: "claude-opus-4-8"}, "anthropic", io.Discard)
	for _, choice := range choices {
		if strings.HasPrefix(choice.ID, "gpt-") {
			t.Fatalf("picker exposed another provider model: %#v", choices)
		}
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
	want := []string{"config", "provider", "credentials", "model", "sessions", "ripgrep", "git", "workspace"}
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

	system, blocks := chatSystem(&config.Config{}, cwd, nil, profile.Profile{}, chatRegistry(), io.Discard)

	if n := blocksContaining(blocks, "# Project instructions"); n != 0 {
		t.Fatalf("project instruction blocks = %d, want 0", n)
	}
	if strings.Contains(system, "must not enter the prompt") {
		t.Fatal("memory.md should not enter the system prompt")
	}
}

func TestSessionBackend_HonorsSavedModelForSameProvider(t *testing.T) {
	cfg := &config.Config{Provider: "openai", Model: "gpt-5.2"}
	meta := session.Metadata{Provider: "openai", Model: "gpt-5-mini"}
	provider, model := sessionBackend(cfg, meta, io.Discard)
	if provider != "openai" || model != "gpt-5-mini" {
		t.Fatalf("session backend = %s/%s, want openai/gpt-5-mini", provider, model)
	}
}

func TestSaveChatSession_NormalizesCodexAdapterProviderForResume(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	authStore := auth.NewStore(filepath.Join(home, ".neo", "auth.json"))
	if err := authStore.Set(auth.ProviderOpenAICodex, auth.Credentials{
		AccessToken: "subscription-token",
		ExpiresAt:   time.Now().Add(time.Hour),
		AccountID:   "account-1",
	}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	store := session.NewStore(t.TempDir())
	sess, err := store.Create(ctx, session.Metadata{Model: "gpt-5-codex"})
	if err != nil {
		t.Fatal(err)
	}
	ag := agent.New(agent.Config{
		Provider: openai.NewCodex(nil),
		Model:    "gpt-5-codex",
	})
	if err := saveChatSession(ctx, store, sess, ag, "/workspace", ""); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ctx, sess.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Metadata.Provider; got != "openai" {
		t.Fatalf("saved provider = %q, want stable provider ID openai", got)
	}
	if got := loaded.Metadata.OpenAIAuth; got != config.OpenAIAuthSubscription {
		t.Fatalf("saved OpenAI auth = %q, want subscription", got)
	}

	cfg := &config.Config{
		Provider:   "anthropic",
		Model:      "claude-opus-4-8",
		OpenAIAuth: config.OpenAIAuthSubscription,
	}
	var warnings bytes.Buffer
	provider, model := sessionBackend(cfg, loaded.Metadata, &warnings)
	if provider != "openai" || model != "gpt-5-codex" {
		t.Fatalf("session backend = %s/%s, want openai/gpt-5-codex", provider, model)
	}
	if warnings.Len() != 0 {
		t.Fatalf("resume warning = %q, want none", warnings.String())
	}
	resumed, err := chatSessionProvider(ctx, cfg, loaded, provider)
	if err != nil {
		t.Fatalf("resume provider: %v", err)
	}
	if got := resumed.Name(); got != "openai-codex" {
		t.Fatalf("resumed adapter = %q, want openai-codex", got)
	}

	t.Setenv("OPENAI_API_KEY", "sk-test")
	apiConfig := &config.Config{
		Provider:   "openai",
		Model:      "gpt-5.2",
		OpenAIAuth: config.OpenAIAuthAPIKey,
	}
	warnings.Reset()
	provider, model = sessionBackend(apiConfig, loaded.Metadata, &warnings)
	if provider != "openai" || model != "gpt-5.2" {
		t.Fatalf("changed-auth backend = %s/%s, want configured openai/gpt-5.2", provider, model)
	}
	if !strings.Contains(warnings.String(), "subscription does not match configured api_key") {
		t.Fatalf("changed-auth warning = %q", warnings.String())
	}
	resumed, err = chatSessionProvider(ctx, apiConfig, loaded, provider)
	if err != nil {
		t.Fatalf("API-key fallback provider: %v", err)
	}
	if got := resumed.Name(); got != "openai" {
		t.Fatalf("fallback adapter = %q, want openai", got)
	}
}

func TestSaveChatSession_PreservesOpenAIAPIProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	ctx := context.Background()
	store := session.NewStore(t.TempDir())
	sess, err := store.Create(ctx, session.Metadata{Model: "gpt-5.2"})
	if err != nil {
		t.Fatal(err)
	}
	ag := agent.New(agent.Config{
		Provider: &openai.Client{},
		Model:    "gpt-5.2",
	})
	if err := saveChatSession(ctx, store, sess, ag, "/workspace", ""); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ctx, sess.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Metadata.Provider; got != "openai" {
		t.Fatalf("saved API-key provider = %q, want openai", got)
	}
	if got := loaded.Metadata.Model; got != "gpt-5.2" {
		t.Fatalf("saved API-key model = %q, want gpt-5.2", got)
	}
	if got := loaded.Metadata.OpenAIAuth; got != config.OpenAIAuthAPIKey {
		t.Fatalf("saved OpenAI auth = %q, want api_key", got)
	}

	apiConfig := &config.Config{
		Provider:   "anthropic",
		Model:      "claude-opus-4-8",
		OpenAIAuth: config.OpenAIAuthAPIKey,
	}
	var warnings bytes.Buffer
	provider, model := sessionBackend(apiConfig, loaded.Metadata, &warnings)
	if provider != "openai" || model != "gpt-5.2" {
		t.Fatalf("session backend = %s/%s, want openai/gpt-5.2", provider, model)
	}
	if warnings.Len() != 0 {
		t.Fatalf("API-key resume warning = %q, want none", warnings.String())
	}
	resumed, err := chatSessionProvider(ctx, apiConfig, loaded, provider)
	if err != nil {
		t.Fatalf("resume API-key provider: %v", err)
	}
	if got := resumed.Name(); got != "openai" {
		t.Fatalf("resumed adapter = %q, want openai", got)
	}

	authStore := auth.NewStore(filepath.Join(home, ".neo", "auth.json"))
	if err := authStore.Set(auth.ProviderOpenAICodex, auth.Credentials{
		AccessToken: "subscription-token",
		ExpiresAt:   time.Now().Add(time.Hour),
		AccountID:   "account-1",
	}); err != nil {
		t.Fatal(err)
	}
	subscriptionConfig := &config.Config{
		Provider:   "openai",
		Model:      "gpt-5-codex",
		OpenAIAuth: config.OpenAIAuthSubscription,
	}
	warnings.Reset()
	provider, model = sessionBackend(subscriptionConfig, loaded.Metadata, &warnings)
	if provider != "openai" || model != "gpt-5-codex" {
		t.Fatalf("changed-auth backend = %s/%s, want configured openai/gpt-5-codex", provider, model)
	}
	if !strings.Contains(warnings.String(), "api_key does not match configured subscription") {
		t.Fatalf("changed-auth warning = %q", warnings.String())
	}
	resumed, err = chatSessionProvider(ctx, subscriptionConfig, loaded, provider)
	if err != nil {
		t.Fatalf("subscription fallback provider: %v", err)
	}
	if got := resumed.Name(); got != "openai-codex" {
		t.Fatalf("fallback adapter = %q, want openai-codex", got)
	}
}

func TestSaveChatSession_PersistsCompactionAndAnswerUsage(t *testing.T) {
	ctx := context.Background()
	store := session.NewStore(t.TempDir())
	sess, err := store.Create(ctx, session.Metadata{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}

	summary := llmtest.Text("summary")
	summary.Usage = llm.Usage{InputTokens: 10, OutputTokens: 20, CacheCreationTokens: 30, CacheReadTokens: 40}
	answer := llmtest.Text("answer")
	answer.Usage = llm.Usage{InputTokens: 1, OutputTokens: 2, CacheCreationTokens: 3, CacheReadTokens: 4}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{summary, answer}}
	messages := make([]llm.Message, 0, 10)
	for i := 0; i < 10; i++ {
		role := llm.RoleUser
		if i%2 == 1 {
			role = llm.RoleAssistant
		}
		messages = append(messages, llm.Message{Role: role, Content: []llm.ContentBlock{{Type: "text", Text: strings.Repeat("x", 400)}}})
	}
	ag := agent.New(agent.Config{
		Model:    "test-model",
		Provider: prov,
		Messages: messages,
		Compactor: compact.Summarizer{
			Provider:      prov,
			Model:         "test-model",
			TriggerTokens: 1,
			KeepRecent:    2,
		},
	})
	if _, err := ag.Send(ctx, "continue"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := saveChatSession(ctx, store, sess, ag, "/workspace", ""); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := store.Load(ctx, sess.Metadata.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := llm.Usage{InputTokens: 11, OutputTokens: 22, CacheCreationTokens: 33, CacheReadTokens: 44}
	if loaded.Usage != want {
		t.Fatalf("persisted usage = %+v, want summary plus answer exactly once: %+v", loaded.Usage, want)
	}
}

func TestSessionBackend_InfersLegacyOpenAIAuthMode(t *testing.T) {
	tests := []struct {
		name           string
		metaProvider   string
		configuredAuth string
		wantModel      string
		wantWarning    bool
	}{
		{name: "Codex under subscription", metaProvider: "openai-codex", configuredAuth: config.OpenAIAuthSubscription, wantModel: "saved-model"},
		{name: "Codex under API key", metaProvider: "openai-codex", configuredAuth: config.OpenAIAuthAPIKey, wantModel: "configured-model", wantWarning: true},
		{name: "OpenAI under API key", metaProvider: "openai", configuredAuth: config.OpenAIAuthAPIKey, wantModel: "saved-model"},
		{name: "OpenAI under subscription", metaProvider: "openai", configuredAuth: config.OpenAIAuthSubscription, wantModel: "configured-model", wantWarning: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Provider: "openai", Model: "configured-model", OpenAIAuth: tt.configuredAuth}
			meta := session.Metadata{Provider: tt.metaProvider, Model: "saved-model"}
			var warnings bytes.Buffer

			provider, model := sessionBackend(cfg, meta, &warnings)

			if provider != "openai" || model != tt.wantModel {
				t.Fatalf("session backend = %s/%s, want openai/%s", provider, model, tt.wantModel)
			}
			if got := warnings.Len() > 0; got != tt.wantWarning {
				t.Fatalf("warning present = %v, want %v: %q", got, tt.wantWarning, warnings.String())
			}
		})
	}
}

func TestSessionBackend_FallsBackWhenSavedProviderCredentialsAreMissing(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cfg := &config.Config{Provider: "anthropic", Model: "claude-opus-4-8"}
	meta := session.Metadata{Provider: "openai", Model: "gpt-5-codex"}
	provider, model := sessionBackend(cfg, meta, io.Discard)
	if provider != "anthropic" || model != "claude-opus-4-8" {
		t.Fatalf("session backend = %s/%s, want anthropic/claude-opus-4-8", provider, model)
	}
}

func TestSessionBackend_RestoresSavedProviderWhenCredentialIsConfigured(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	cfg := &config.Config{Provider: "anthropic", Model: "claude-opus-4-8"}
	meta := session.Metadata{Provider: "openai", Model: "gpt-5.2"}
	provider, model := sessionBackend(cfg, meta, io.Discard)
	if provider != "openai" || model != "gpt-5.2" {
		t.Fatalf("session backend = %s/%s, want openai/gpt-5.2", provider, model)
	}
}

func TestSessionBackend_FallsBackForLegacySessionsWithoutProvider(t *testing.T) {
	// Sessions written before the provider field existed must not pin a model
	// that may belong to a different backend.
	cfg := &config.Config{Provider: "anthropic", Model: "claude-opus-4-8"}
	meta := session.Metadata{Model: "gpt-4o"}
	provider, model := sessionBackend(cfg, meta, io.Discard)
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

func TestParseHeadlessArgsDefaults(t *testing.T) {
	opts, prompt, err := parseHeadlessArgs([]string{"Review", "the", "repo"}, nil)
	if err != nil {
		t.Fatalf("parseHeadlessArgs returned error: %v", err)
	}
	if prompt != "Review the repo" {
		t.Fatalf("prompt = %q", prompt)
	}
	if opts.timeout != 10*time.Minute {
		t.Fatalf("timeout = %v, want 10m", opts.timeout)
	}
}

func TestParseHeadlessArgsConfigAndModelOverrides(t *testing.T) {
	for _, args := range [][]string{
		{"--config", "headless.yaml", "--model", "override-model", "prompt"},
		{"--config=headless.yaml", "--model=override-model", "prompt"},
	} {
		opts, prompt, err := parseHeadlessArgs(args, nil)
		if err != nil {
			t.Fatalf("parseHeadlessArgs(%q): %v", args, err)
		}
		if opts.configPath != "headless.yaml" || opts.model != "override-model" || !opts.modelSet {
			t.Fatalf("options = %+v", opts)
		}
		if prompt != "prompt" {
			t.Fatalf("prompt = %q", prompt)
		}
	}
}

func TestParseHeadlessArgsRejectsEmptyModel(t *testing.T) {
	for _, args := range [][]string{
		{"--model", "", "prompt"},
		{"--model=", "prompt"},
		{"--model", "--config", "headless.yaml", "prompt"},
	} {
		_, _, err := parseHeadlessArgs(args, nil)
		if err == nil || !strings.Contains(err.Error(), "--model needs a non-empty id") {
			t.Fatalf("parseHeadlessArgs(%q) error = %v", args, err)
		}
	}
}

func TestParseHeadlessArgsRejectsMissingConfigPath(t *testing.T) {
	for _, args := range [][]string{
		{"--config", "", "prompt"},
		{"--config=", "prompt"},
		{"--config", "--model", "override-model", "prompt"},
	} {
		_, _, err := parseHeadlessArgs(args, nil)
		if err == nil || !strings.Contains(err.Error(), "--config needs a path") {
			t.Fatalf("parseHeadlessArgs(%q) error = %v", args, err)
		}
	}
}

func TestParseHeadlessArgsPreservesFlagLikePromptTextAndDashPrefixedValues(t *testing.T) {
	for _, test := range []struct {
		name       string
		args       []string
		configPath string
		model      string
		prompt     string
	}{
		{
			name:   "prompt after separator",
			args:   []string{"--", "--config", "--model"},
			prompt: "--config --model",
		},
		{
			name:   "prompt after first positional argument",
			args:   []string{"prompt", "--config", "--model"},
			prompt: "prompt --config --model",
		},
		{
			name:       "dash prefixed config path",
			args:       []string{"--config", "-ci.yaml", "prompt"},
			configPath: "-ci.yaml",
			prompt:     "prompt",
		},
		{
			name:   "dash prefixed model id",
			args:   []string{"--model", "-test-model", "prompt"},
			model:  "-test-model",
			prompt: "prompt",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts, prompt, err := parseHeadlessArgs(test.args, nil)
			if err != nil {
				t.Fatalf("parseHeadlessArgs(%q): %v", test.args, err)
			}
			if opts.configPath != test.configPath || opts.model != test.model {
				t.Fatalf("options = %+v", opts)
			}
			if prompt != test.prompt {
				t.Fatalf("prompt = %q, want %q", prompt, test.prompt)
			}
		})
	}
}

func TestLoadHeadlessConfigPrefersExplicitFile(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile(filepath.Join(root, "neo.yaml"), []byte("model: discovered-model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	explicit := filepath.Join(root, "headless.yaml")
	if err := os.WriteFile(explicit, []byte("model: explicit-model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, ok := loadHeadlessConfig(headlessOptions{configPath: explicit}, io.Discard)
	if !ok {
		t.Fatal("loadHeadlessConfig returned false")
	}
	if cfg.Model != "explicit-model" {
		t.Fatalf("model = %q, want explicit model", cfg.Model)
	}
}

func TestLoadHeadlessConfigReportsExplicitFileFailure(t *testing.T) {
	var errOut bytes.Buffer
	_, ok := loadHeadlessConfig(headlessOptions{configPath: filepath.Join(t.TempDir(), "missing.yaml")}, &errOut)
	if ok || !strings.Contains(errOut.String(), "config:") || !strings.Contains(errOut.String(), "missing.yaml") {
		t.Fatalf("ok = %v, stderr = %q", ok, errOut.String())
	}
}

func TestParseHeadlessArgsRejectsRemovedPermissionFlag(t *testing.T) {
	for _, args := range [][]string{
		{"--permission", "readonly", "prompt"},
		{"prompt", "--permission", "readonly"},
		{"--permission=readonly", "prompt"},
		{"prompt", "-permission=readonly"},
	} {
		_, _, err := parseHeadlessArgs(args, nil)
		if err == nil || !strings.Contains(err.Error(), "--permission has been removed") {
			t.Fatalf("parseHeadlessArgs(%q) error = %v, want removed-option error", args, err)
		}
	}
}

func TestHeadlessRegistryIncludesWritableTools(t *testing.T) {
	names := newRegistry(t.TempDir(), t.TempDir()).Names()
	for _, want := range []string{"bash", "edit_file", "write_file"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("headless registry %v missing %q", names, want)
		}
	}
}

func TestHeadlessResultOmitsPermissionField(t *testing.T) {
	out, err := json.Marshal(headlessResult{OK: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "permission") {
		t.Fatalf("headless JSON still contains removed permission field: %s", out)
	}
}

func TestParseHeadlessArgsRequiresPrompt(t *testing.T) {
	_, _, err := parseHeadlessArgs([]string{"--json"}, nil)
	if err == nil {
		t.Fatal("expected missing prompt error")
	}
}

func TestParseHeadlessArgsReadsInjectedPipedInput(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	if _, err := input.WriteString("from stdin\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	_, prompt, err := parseHeadlessArgs([]string{"from", "args"}, input)
	if err != nil {
		t.Fatal(err)
	}
	if prompt != "from stdin from args" {
		t.Fatalf("prompt = %q, want piped stdin followed by arguments", prompt)
	}
}

func TestTakeAgentFlag(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantArgs []string
		wantName string
		wantErr  bool
	}{
		{name: "absent", args: []string{"run", "hello"}, wantArgs: []string{"run", "hello"}},
		{name: "equals form", args: []string{"--agent=reviewer"}, wantName: "reviewer"},
		{name: "space form", args: []string{"--agent", "reviewer"}, wantName: "reviewer"},
		{name: "single dash", args: []string{"-agent=reviewer"}, wantName: "reviewer"},
		{
			name:     "keeps other args in order",
			args:     []string{"run", "--agent=reviewer", "--json", "do the thing"},
			wantArgs: []string{"run", "--json", "do the thing"},
			wantName: "reviewer",
		},
		{name: "missing value", args: []string{"--agent"}, wantErr: true},
		{name: "empty value", args: []string{"--agent="}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, name, err := takeAgentFlag(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if name != tc.wantName {
				t.Fatalf("name = %q, want %q", name, tc.wantName)
			}
			if strings.Join(args, "|") != strings.Join(tc.wantArgs, "|") {
				t.Fatalf("args = %v, want %v", args, tc.wantArgs)
			}
		})
	}
}

func TestChatSystem_AgentProfileReplacesTheBuiltInPrompt(t *testing.T) {
	cwd := t.TempDir()
	agentProfile := profile.Profile{Name: "assistant", Body: "You are a calm personal assistant."}

	system, blocks := chatSystem(&config.Config{}, cwd, nil, agentProfile, chatRegistry(), io.Discard)

	if !strings.Contains(system, agentProfile.Body) {
		t.Fatalf("profile body missing:\n%s", system)
	}
	if strings.Contains(system, "You are neo, a coding agent") {
		t.Fatalf("the built-in prompt must be replaced, not appended:\n%s", system)
	}
	// Everything after the base block still composes.
	if !strings.Contains(system, "# Environment") {
		t.Fatalf("environment section missing:\n%s", system)
	}
	if len(blocks) == 0 || !blocks[0].Cache {
		t.Fatalf("the profile belongs in the cacheable base block: %+v", blocks)
	}
}

func TestChatSystem_NoProfileKeepsTheCodingPrompt(t *testing.T) {
	system, _ := chatSystem(&config.Config{}, t.TempDir(), nil, profile.Profile{}, chatRegistry(), io.Discard)
	if !strings.Contains(system, "You are neo, a coding agent") {
		t.Fatalf("default prompt changed:\n%s", system)
	}
}

func TestSaveChatSession_RecordsTheActiveAgent(t *testing.T) {
	ctx := context.Background()
	store := session.NewStore(t.TempDir())
	sess, err := store.Create(ctx, session.Metadata{Agent: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	ag := agent.New(agent.Config{Model: "m", Provider: &llmtest.FakeProvider{}})

	// Resuming under a different profile switches the session's agent.
	if err := saveChatSession(ctx, store, sess, ag, "/workspace", "reviewer"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.Load(ctx, sess.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Metadata.Agent != "reviewer" {
		t.Fatalf("agent = %q, want reviewer", reloaded.Metadata.Agent)
	}
}
