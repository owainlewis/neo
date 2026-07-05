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
		if cfg.Model == "" {
			t.Fatal("embedded config must default a model")
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

func TestLoad_ValidOpenAIAuthIgnoredForAnthropicProvider(t *testing.T) {
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
			t.Fatal("openai_auth must be ignored for non-openai providers")
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

func TestFeatures_PromptCommandsDefaultsOnExplicitFalseDisables(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.PromptCommandsEnabled() {
			t.Fatal("expected prompt commands to default on when omitted")
		}

		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\nfeatures:\n  prompt_commands: false\n")
		cfg, err = Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.PromptCommandsEnabled() {
			t.Fatal("expected prompt commands disabled when set to false")
		}
	})
}

func TestFeatures_MemoryDefaultsOnExplicitFalseDisables(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !cfg.MemoryEnabled() {
			t.Fatal("expected memory to default on when omitted")
		}

		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\nfeatures:\n  memory: false\n")
		cfg, err = Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.MemoryEnabled() {
			t.Fatal("expected memory disabled when set to false")
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

func TestPermissions_DefaultsToTrusted(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\n")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Permissions.Mode != PermissionModeTrusted {
			t.Fatalf("permissions.mode = %q, want %q", cfg.Permissions.Mode, PermissionModeTrusted)
		}
	})
}

func TestPermissions_AcceptsKnownModes(t *testing.T) {
	for _, mode := range []string{PermissionModeAsk, PermissionModeTrusted, PermissionModeReadonly} {
		t.Run(mode, func(t *testing.T) {
			withTempDir(t, func(dir string) {
				t.Setenv("HOME", dir)
				writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\npermissions:\n  mode: "+mode+"\n")
				cfg, err := Load()
				if err != nil {
					t.Fatalf("load: %v", err)
				}
				if cfg.Permissions.Mode != mode {
					t.Fatalf("permissions.mode = %q, want %q", cfg.Permissions.Mode, mode)
				}
			})
		})
	}
}

func TestPermissions_RejectsUnknownMode(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "model: m\npermissions:\n  mode: nope\n")
		if _, err := Load(); err == nil {
			t.Fatal("expected invalid permissions.mode to fail")
		}
	})
}
