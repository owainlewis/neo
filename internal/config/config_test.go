package config

import (
	"os"
	"path/filepath"
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
