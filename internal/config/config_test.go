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
