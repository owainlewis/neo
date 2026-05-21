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
		// Embedded defaults must include the `code` flow.
		if _, ok := cfg.Flows["code"]; !ok {
			t.Fatalf("expected embedded `code` flow, got %v", cfg.FlowNames())
		}
	})
}

func TestLoad_PrefersProjectConfig(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), `
model: project-model
flows:
  custom:
    steps: [foo, bar]
`)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Source() != "neo.yaml" {
			t.Fatalf("expected source 'neo.yaml', got %q", cfg.Source())
		}
		if cfg.Model != "project-model" {
			t.Fatalf("model: got %q", cfg.Model)
		}
		if _, ok := cfg.Flows["custom"]; !ok {
			t.Fatalf("missing custom flow; got %v", cfg.FlowNames())
		}
		// First-hit-wins: the embedded `code` flow must NOT leak through.
		if _, ok := cfg.Flows["code"]; ok {
			t.Fatalf("project config should not be merged with embedded defaults")
		}
	})
}

func TestLoad_RejectsInvalidYAML(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		writeFile(t, filepath.Join(dir, "neo.yaml"), "flows: {{{")
		if _, err := Load(); err == nil {
			t.Fatal("expected parse error")
		}
	})
}

func TestValidate_RejectsEmptySteps(t *testing.T) {
	c := &Config{Flows: map[string]FlowConfig{
		"empty": {Steps: nil},
	}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error on empty steps")
	}
}

func TestValidate_RejectsRetryFromOutsideSteps(t *testing.T) {
	c := &Config{Flows: map[string]FlowConfig{
		"f": {Steps: []string{"a", "b"}, RetryFrom: "nope"},
	}}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error on bad retry_from")
	}
	if !strings.Contains(err.Error(), "retry_from") {
		t.Fatalf("error should mention retry_from, got %v", err)
	}
}

func TestValidate_RejectsNegativeMaxRounds(t *testing.T) {
	c := &Config{Flows: map[string]FlowConfig{
		"f": {Steps: []string{"a"}, MaxRounds: -1},
	}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error on negative max_rounds")
	}
}

func TestResolveStep_ProjectOverridesEmbedded(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		// Create a project flows/write.md that overrides the embedded one.
		flowsDir := filepath.Join(dir, "flows")
		if err := os.MkdirAll(flowsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(flowsDir, "write.md"), "OVERRIDDEN WRITE STEP")

		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		def, err := cfg.ResolveStep("write")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if def.Prompt != "OVERRIDDEN WRITE STEP" {
			t.Fatalf("project override not used; got prompt %q", def.Prompt)
		}
	})
}

func TestResolveStep_NotFoundListsSearchedPaths(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		_, err = cfg.ResolveStep("does-not-exist")
		if err == nil {
			t.Fatal("expected error")
		}
		var nf *StepNotFoundError
		if !errAs(err, &nf) {
			t.Fatalf("expected StepNotFoundError, got %T: %v", err, err)
		}
		if len(nf.Searched) < 2 {
			t.Fatalf("expected searched paths in error, got %v", nf.Searched)
		}
	})
}

func TestResolveStep_ParsesFrontmatter(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		flowsDir := filepath.Join(dir, "flows")
		os.MkdirAll(flowsDir, 0o755)
		writeFile(t, filepath.Join(flowsDir, "custom.md"), `---
tools: [bash, read_file]
model: claude-haiku-4-5
---

You are the CUSTOM step.
Do the thing.`)

		cfg, _ := Load()
		def, err := cfg.ResolveStep("custom")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if len(def.Tools) != 2 || def.Tools[0] != "bash" {
			t.Fatalf("tools not parsed: %+v", def.Tools)
		}
		if def.Model != "claude-haiku-4-5" {
			t.Fatalf("model not parsed: %q", def.Model)
		}
		if !strings.Contains(def.Prompt, "You are the CUSTOM step") {
			t.Fatalf("prompt body not extracted: %q", def.Prompt)
		}
		if strings.Contains(def.Prompt, "tools:") {
			t.Fatalf("frontmatter leaked into prompt: %q", def.Prompt)
		}
	})
}

func TestResolveStep_UnterminatedFrontmatterErrors(t *testing.T) {
	withTempDir(t, func(dir string) {
		t.Setenv("HOME", dir)
		flowsDir := filepath.Join(dir, "flows")
		os.MkdirAll(flowsDir, 0o755)
		writeFile(t, filepath.Join(flowsDir, "broken.md"), `---
tools: [bash]
prompt body without closing fence`)
		cfg, _ := Load()
		_, err := cfg.ResolveStep("broken")
		if err == nil {
			t.Fatal("expected unterminated frontmatter error")
		}
	})
}

func TestFlowNames_Sorted(t *testing.T) {
	c := &Config{Flows: map[string]FlowConfig{
		"zebra":  {Steps: []string{"a"}},
		"apple":  {Steps: []string{"a"}},
		"middle": {Steps: []string{"a"}},
	}}
	names := c.FlowNames()
	want := []string{"apple", "middle", "zebra"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("FlowNames not sorted: got %v", names)
		}
	}
}

// errAs is a tiny generic wrapper around errors.As to keep tests terse.
func errAs[T error](err error, target *T) bool {
	for err != nil {
		if t, ok := err.(T); ok {
			*target = t
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
