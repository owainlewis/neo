package factory

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestResolveAgentStepWithFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "review.md"), `---
tools: [bash, read_file]
model: claude-opus-4-8
max_turns: 7
---
Review the thing.`, 0o644)

	st, err := Resolver{Paths: []string{dir}}.Resolve("review")
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind != "agent" || st.Prompt != "Review the thing." {
		t.Fatalf("unexpected step: %+v", st)
	}
	if st.Model != "claude-opus-4-8" || st.MaxTurns != 7 {
		t.Fatalf("frontmatter not parsed: %+v", st)
	}
	if !slices.Equal(st.Tools, []string{"bash", "read_file"}) {
		t.Fatalf("tools = %v", st.Tools)
	}
}

func TestResolveScriptStepRequiresExecBit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "checks"), "#!/bin/sh\necho ok\n", 0o755)
	writeFile(t, filepath.Join(dir, "notexec"), "#!/bin/sh\necho no\n", 0o644)

	r := Resolver{Paths: []string{dir}}
	st, err := r.Resolve("checks")
	if err != nil || st.Kind != "script" {
		t.Fatalf("checks: %+v, %v", st, err)
	}
	if _, err := r.Resolve("notexec"); err == nil {
		t.Fatal("non-executable file resolved as a step")
	}
}

func TestResolveRejectsPathTraversal(t *testing.T) {
	r := Resolver{}
	for _, name := range []string{"../etc/passwd", "a/b", `a\b`, "name.md", ""} {
		if _, err := r.Resolve(name); err == nil {
			t.Fatalf("name %q resolved; want error", name)
		}
	}
}

func TestResolveByFrontmatterName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "step1.md"), `---
name: first
tools: [read_file]
---
Step one.`, 0o644)

	r := Resolver{Paths: []string{dir}}
	st, err := r.Resolve("first")
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "first" || st.Prompt != "Step one." {
		t.Fatalf("unexpected step: %+v", st)
	}
	// A frontmatter name is the step's only name.
	if _, err := r.Resolve("step1"); err == nil {
		t.Fatal("renamed step still answers to its filename")
	}
	if names := r.List(); !slices.Contains(names, "first") || slices.Contains(names, "step1") {
		t.Fatalf("List() = %v", names)
	}
}

func TestResolveProjectOverridesEmbedded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "worker.md"), "Custom worker.", 0o644)

	st, err := Resolver{Paths: []string{dir}}.Resolve("worker")
	if err != nil {
		t.Fatal(err)
	}
	if st.Prompt != "Custom worker." {
		t.Fatalf("project step did not override embedded: %q", st.Prompt)
	}
}

func TestResolveEmbeddedDefaults(t *testing.T) {
	r := Resolver{}
	for _, name := range []string{"orchestrator", "worker", "verify", "triage"} {
		st, err := r.Resolve(name)
		if err != nil {
			t.Fatalf("embedded %s: %v", name, err)
		}
		if st.Kind != "agent" || st.Prompt == "" || len(st.Tools) == 0 {
			t.Fatalf("embedded %s incomplete: %+v", name, st)
		}
	}
	// Only the orchestrator and worker may delegate.
	for name, want := range map[string]bool{"orchestrator": true, "worker": true, "verify": false, "triage": false} {
		st, _ := r.Resolve(name)
		if got := slices.Contains(st.Tools, "run_step"); got != want {
			t.Errorf("%s run_step grant = %v, want %v", name, got, want)
		}
	}
	// Read-only roles must not carry write tools.
	for _, name := range []string{"verify", "triage"} {
		st, _ := r.Resolve(name)
		for _, tool := range st.Tools {
			if tool == "write_file" || tool == "edit_file" {
				t.Errorf("%s grants %s; verification must be read-only", name, tool)
			}
		}
	}
}

func TestCatalog(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "checks"), "#!/bin/sh\n", 0o755)
	writeFile(t, filepath.Join(dir, "custom.md"), `---
description: "a custom step"
---
Hi.`, 0o644)
	// Project worker overrides the embedded default.
	writeFile(t, filepath.Join(dir, "worker.md"), `---
description: "project worker"
---
Work.`, 0o644)

	cat := Resolver{Paths: []string{dir}}.Catalog()
	byName := map[string]Step{}
	for _, st := range cat {
		byName[st.Name] = st
	}
	if st := byName["custom"]; st.Description != "a custom step" || st.Kind != "agent" {
		t.Fatalf("custom = %+v", st)
	}
	if st := byName["checks"]; st.Kind != "script" || st.Description != "" {
		t.Fatalf("checks = %+v", st)
	}
	if st := byName["worker"]; st.Description != "project worker" {
		t.Fatalf("project step did not win: %+v", st)
	}
	// Embedded defaults all carry descriptions.
	for _, name := range []string{"orchestrator", "verify", "triage"} {
		if byName[name].Description == "" {
			t.Errorf("embedded %s has no description", name)
		}
	}
}

func TestListIncludesAllSources(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "checks"), "#!/bin/sh\n", 0o755)
	writeFile(t, filepath.Join(dir, "custom.md"), "Hi.", 0o644)

	names := Resolver{Paths: []string{dir}}.List()
	for _, want := range []string{"checks", "custom", "orchestrator", "worker", "verify", "triage"} {
		if !slices.Contains(names, want) {
			t.Errorf("List() missing %q: %v", want, names)
		}
	}
}
