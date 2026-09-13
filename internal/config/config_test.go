package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempDir runs fn from a temp working directory so project-relative
// config lookups can't pick up the repo's actual neo.yaml.
func withTempDir(t *testing.T, fn func(dir string)) {
	t.Helper()
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	fn(dir)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigPathsSkipUserConfigWhenHomeIsEmpty(t *testing.T) {
	got := configPaths("")
	if len(got) != 1 || got[0] != projectConfigName {
		t.Fatalf("config paths = %#v, want only %q", got, projectConfigName)
	}
}

func TestLoad_FallsBackToEmbeddedWhenNoLocalConfig(t *testing.T) {
	withTempDir(t, func(dir string) {
		// Force the HOME lookup to a place with no config.
		t.Setenv("HOME", dir)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Source() != "embedded" {
			t.Fatalf("expected embedded source, got %q", cfg.Source())
		}
		if cfg.Model != defaultModel {
			t.Fatalf("embedded model: got %q want %q", cfg.Model, defaultModel)
		}
	})
}

func TestLoad_RejectsRemovedPhasesKey(t *testing.T) {
	withTempDir(t, func(dir string) {
		writeFile(t, filepath.Join(dir, "neo.yaml"), "phases:\n  security:\n    prompt: inspect\n")
		_, err := Load()
		if err == nil {
			t.Fatal("expected removed phases key to fail")
		}
		for _, want := range []string{"neo.yaml", "phases has been removed", ".neo/skills"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
	})
}

func TestLoad_PrefersProjectConfig(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: project-model\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Source() != "neo.yaml" {
			t.Fatalf("expected source 'neo.yaml', got %q", cfg.Source())
		}
		if cfg.Model != "project-model" {
			t.Fatalf("expected project model, got %q", cfg.Model)
		}
	})
}

func TestLoad_RejectsInvalidYAML(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: [unclosed\n")
		if _, err := Load(); err == nil {
			t.Fatal("expected error on malformed yaml")
		}
	})
}

func TestLoad_DefaultsProviderToAnthropic(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Provider != "anthropic" {
			t.Fatalf("expected default provider anthropic, got %q", cfg.Provider)
		}
	})
}

func TestLoad_OpenAIProviderGetsOpenAIDefaultModel(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openai\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Provider != "openai" {
			t.Fatalf("provider: got %q want openai", cfg.Provider)
		}
		if cfg.Model != defaultOpenAIModel {
			t.Fatalf("model: got %q want %q", cfg.Model, defaultOpenAIModel)
		}
	})
}

func TestLoad_OpenRouterProviderGetsOpenRouterDefaultModel(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openrouter\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Provider != "openrouter" {
			t.Fatalf("provider: got %q want openrouter", cfg.Provider)
		}
		if cfg.Model != defaultOpenRouterModel {
			t.Fatalf("model: got %q want %q", cfg.Model, defaultOpenRouterModel)
		}
	})
}

func TestLoad_GoogleProviderGetsGoogleDefaultModel(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: google\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Provider != "google" {
			t.Fatalf("provider: got %q want google", cfg.Provider)
		}
		if cfg.Model != defaultGoogleModel {
			t.Fatalf("model: got %q want %q", cfg.Model, defaultGoogleModel)
		}
	})
}

func TestLoad_OpenAIDefaultsToAPIKeyAuth(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openai\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.OpenAIAuth != OpenAIAuthAPIKey {
			t.Fatalf("openai_auth: got %q want api_key", cfg.OpenAIAuth)
		}
		if cfg.SubscriptionAuth() {
			t.Fatal("api_key auth must not report SubscriptionAuth")
		}
	})
}

func TestLoad_OpenAIAcceptsExplicitAPIKeyAuth(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openai\nopenai_auth: api_key\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.OpenAIAuth != OpenAIAuthAPIKey {
			t.Fatalf("openai_auth: got %q want %q", cfg.OpenAIAuth, OpenAIAuthAPIKey)
		}
		if cfg.SubscriptionAuth() {
			t.Fatal("api_key auth must not report SubscriptionAuth")
		}
	})
}

func TestLoad_OpenAISubscriptionGetsCodexModel(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openai\nopenai_auth: subscription\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.SubscriptionAuth() {
			t.Fatal("expected SubscriptionAuth to be true")
		}
		if cfg.Model != defaultCodexModel {
			t.Fatalf("model: got %q want %q", cfg.Model, defaultCodexModel)
		}
	})
}

func TestLoad_RejectsInvalidOpenAIAuthMode(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "openai_auth: nope\n")
		_, err := Load()
		if err == nil {
			t.Fatal("expected invalid openai_auth to fail")
		}
		for _, want := range []string{"neo.yaml", "openai_auth", OpenAIAuthAPIKey, OpenAIAuthSubscription, "nope"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not contain %q", err.Error(), want)
			}
		}
	})
}

func TestLoad_OpenAIAuthDoesNotChangeAnthropicStartupDefault(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: anthropic\nopenai_auth: subscription\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Provider != "anthropic" {
			t.Fatalf("provider: got %q want anthropic", cfg.Provider)
		}
		if cfg.SubscriptionAuth() {
			t.Fatal("anthropic startup must not use OpenAI subscription auth")
		}
		if cfg.Model != defaultModel {
			t.Fatalf("model: got %q want %q", cfg.Model, defaultModel)
		}
	})
}

func TestLoad_ExplicitModelOverridesProviderDefault(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openai\nmodel: gpt-custom\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Model != "gpt-custom" {
			t.Fatalf("model: got %q want gpt-custom", cfg.Model)
		}
	})
}

func TestLoad_SubagentsBackendDefaultsToCoordinator(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "provider: openai\nmodel: gpt-main\nsubagents:\n  model: gpt-worker\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.SubagentsConfigured() {
			t.Fatal("subagents backend should be configured")
		}
		if cfg.Subagents.Provider != "openai" || cfg.Subagents.Model != "gpt-worker" {
			t.Fatalf("subagents backend = %#v", cfg.Subagents)
		}
	})
}

func TestLoad_SubagentsProviderGetsProviderDefaultModel(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "subagents:\n  provider: google\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Subagents.Provider != "google" || cfg.Subagents.Model != defaultGoogleModel {
			t.Fatalf("subagents backend = %#v", cfg.Subagents)
		}
	})
}

func TestLoad_SubagentsBackendAbsentFollowsCoordinator(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: main-model\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.SubagentsConfigured() {
			t.Fatalf("subagents backend unexpectedly configured: %#v", cfg.Subagents)
		}
	})
}

func TestLoad_RejectsUnknownSubagentsProvider(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "subagents:\n  provider: mystery\n  model: worker\n")
		_, err := Load()
		if err == nil {
			t.Fatal("expected unknown subagents provider to fail")
		}
		for _, want := range []string{"neo.yaml", "subagents.provider", "mystery"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not contain %q", err.Error(), want)
			}
		}
	})
}

func TestLoad_CompactionContextWindowOverride(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\ncompaction:\n  context_window_tokens: 1000000\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Compaction.ContextWindowTokens != 1_000_000 {
			t.Fatalf("context_window_tokens = %d, want 1000000", cfg.Compaction.ContextWindowTokens)
		}
	})
}

func TestFeatures_AgentsFileDefaultsOnWhenAbsent(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.AgentsFileEnabled() {
			t.Fatal("expected agents_file to default on when omitted")
		}
	})
}

func TestFeatures_AgentsFileExplicitFalseDisables(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\nfeatures:\n  agents_file: false\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.AgentsFileEnabled() {
			t.Fatal("expected agents_file disabled when set to false")
		}
	})
}

func TestFeatures_SkillsDefaultsOnExplicitFalseDisables(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.SkillsEnabled() {
			t.Fatal("expected skills to default on when omitted")
		}

		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\nfeatures:\n  skills: false\n")
		cfg, err = Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.SkillsEnabled() {
			t.Fatal("expected skills disabled when set to false")
		}
	})
}

func TestOutput_VerboseDefaultsOffWhenAbsent(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.VerboseEnabled() {
			t.Fatal("expected output.verbose to default off when omitted")
		}
	})
}

func TestOutput_VerboseExplicitTrueEnables(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\noutput:\n  verbose: true\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.VerboseEnabled() {
			t.Fatal("expected output.verbose: true to enable verbose output")
		}
	})
}

func TestOutput_VerboseExplicitFalseStaysOff(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\noutput:\n  verbose: false\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.VerboseEnabled() {
			t.Fatal("expected output.verbose: false to stay off")
		}
	})
}

func TestFeatures_LegacyMemoryKeyIsIgnored(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\nfeatures:\n  memory: true\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Model != "m" {
			t.Fatalf("model = %q, want m", cfg.Model)
		}
	})
}

func TestFeatures_PromptCachingDefaultsOnExplicitFalseDisables(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.PromptCachingEnabled() {
			t.Fatal("expected prompt_caching to default on when omitted")
		}

		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\nfeatures:\n  prompt_caching: false\n")
		cfg, err = Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.PromptCachingEnabled() {
			t.Fatal("expected prompt_caching disabled when set to false")
		}
	})
}

func TestToolApprovals_DefaultsEmpty(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(cfg.ToolApprovals) != 0 {
			t.Fatalf("tool_approvals = %#v, want empty", cfg.ToolApprovals)
		}
	})
}

func TestToolApprovals_TrimsAndDeduplicates(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\ntool_approvals:\n  - ' git '\n  - write_file\n  - git\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(cfg.ToolApprovals) != 2 || cfg.ToolApprovals[0] != "git" || cfg.ToolApprovals[1] != "write_file" {
			t.Fatalf("tool_approvals = %#v", cfg.ToolApprovals)
		}
	})
}

func TestToolApprovals_RejectsEmptyEntry(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\ntool_approvals:\n  - '   '\n")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "must not be empty") {
			t.Fatalf("error = %v, want empty-entry error", err)
		}
	})
}

func TestPermissions_RejectsRemovedConfigWithMigration(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\npermissions:\n  mode: readonly\n")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "use tool_approvals") {
			t.Fatalf("error = %v, want migration error", err)
		}
	})
}

func TestLoad_MergesLayers(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		if err := os.MkdirAll(filepath.Join(dir, ".neo"), 0o755); err != nil {
			t.Fatal(err)
		}
		global := filepath.Join(dir, ".neo", "config.yaml")
		writeFile(t, global, `provider: openai
openai_auth: subscription
model: global-model
subagents:
  provider: google
  model: global-worker
features:
  agents_file: false
  skills: false
output:
  verbose: true
tool_approvals: [git, write_file]
`)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Source() != global || cfg.Model != "global-model" || cfg.Compaction.ContextWindowTokens != 200000 {
			t.Fatalf("global config did not inherit defaults: %#v", cfg)
		}
		writeFile(t, "neo.yaml", `model: project-model
subagents:
  model: project-worker
features:
  skills: true
output:
  verbose: false
`)
		cfg, err = Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Source() != "neo.yaml" || cfg.Model != "project-model" || !cfg.SubscriptionAuth() {
			t.Fatalf("project backend: %#v", cfg)
		}
		if cfg.Subagents.Provider != "google" || cfg.Subagents.Model != "project-worker" {
			t.Fatalf("subagent merge: %#v", cfg.Subagents)
		}
		if cfg.AgentsFileEnabled() || !cfg.SkillsEnabled() || !cfg.PromptCachingEnabled() || cfg.VerboseEnabled() {
			t.Fatal("nested flags did not inherit or override correctly")
		}
		if len(cfg.ToolApprovals) != 2 || cfg.Compaction.ContextWindowTokens != 200000 {
			t.Fatalf("inherited settings: %#v", cfg)
		}
		t.Run("null resets", func(t *testing.T) {
			writeFile(t, "neo.yaml", "features:\n  skills: null\noutput:\n  verbose: null\ntool_approvals: null\n")
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Features.Skills != nil || !cfg.SkillsEnabled() {
				t.Fatal("null skills did not reset inherited false to the built-in default")
			}
			if cfg.Output.Verbose != nil || cfg.VerboseEnabled() {
				t.Fatal("null verbose did not reset inherited true to the built-in default")
			}
			if len(cfg.ToolApprovals) != 0 {
				t.Fatalf("null approvals did not clear inherited list: %v", cfg.ToolApprovals)
			}
			if cfg.AgentsFileEnabled() || cfg.Model != "global-model" {
				t.Fatal("null resets changed omitted inherited settings")
			}
		})
		for _, tc := range []struct {
			name, project, model string
			approvals            int
		}{
			{"empty file", "", "global-model", 2},
			{"replace list", "tool_approvals: [read_file]\n", "global-model", 1},
			{"clear list", "tool_approvals: []\n", "global-model", 0},
			{"inherited explicit model", "provider: google\n", "global-model", 2},
			{"reset model", "provider: google\nmodel: \"\"\n", defaultGoogleModel, 2},
		} {
			t.Run(tc.name, func(t *testing.T) {
				writeFile(t, "neo.yaml", tc.project+"compaction:\n  context_window_tokens: 0\n")
				cfg, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Model != tc.model || len(cfg.ToolApprovals) != tc.approvals || cfg.Compaction.ContextWindowTokens != 0 {
					t.Fatalf("merged config: %#v", cfg)
				}
			})
		}
	})
}

func TestLoad_ResolvesDefaultsAfterMerging(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		if err := os.MkdirAll(filepath.Join(dir, ".neo"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, ".neo", "config.yaml"), "provider: openai\nsubagents:\n  model: worker\n")
		writeFile(t, "neo.yaml", "openai_auth: subscription\n")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Model != defaultCodexModel || cfg.Subagents.Provider != "openai" || cfg.Subagents.Model != "worker" {
			t.Fatalf("derived defaults: %#v", cfg)
		}
	})
}

func TestLoad_DoesNotHideInvalidGlobalConfig(t *testing.T) {
	for _, body := range []string{"model: [unclosed", "permissions: {}", "phases: {}", "openai_auth: invalid", "tool_approvals: ['']", "features: wrong-type"} {
		t.Run(body, func(t *testing.T) {
			withTempDir(t, func(dir string) {
				t.Setenv("HOME", dir)
				if err := os.MkdirAll(filepath.Join(dir, ".neo"), 0o755); err != nil {
					t.Fatal(err)
				}
				global := filepath.Join(dir, ".neo", "config.yaml")
				writeFile(t, global, body)
				writeFile(t, "neo.yaml", "model: valid\n")
				if _, err := Load(); err == nil || !strings.Contains(err.Error(), global) {
					t.Fatalf("expected global config error, got %v", err)
				}
			})
		})
	}
}
