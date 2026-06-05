package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistrySpecsAreSorted(t *testing.T) {
	reg := NewRegistry(WriteFile{}, Bash{}, ReadFile{})
	specs := reg.Specs()
	got := []string{specs[0].Name, specs[1].Name, specs[2].Name}
	want := []string{"bash", "read_file", "write_file"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("spec order = %v, want %v", got, want)
		}
	}
}

func TestGrepSearchesWithContextAndMaxMatches(t *testing.T) {
	root := t.TempDir()
	writeSearchFile(t, filepath.Join(root, "a.txt"), "before\nneedle one\nafter\nneedle two\n")
	out, err := (Grep{Root: root}).Run(context.Background(), map[string]any{
		"pattern":       "needle",
		"context_lines": 1.0,
		"max_matches":   1.0,
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	for _, want := range []string{"a.txt:1:before", "a.txt>2:needle one", "a.txt:3:after", "truncated after 1 matches"} {
		if !strings.Contains(out, want) {
			t.Fatalf("grep output missing %q:\n%s", want, out)
		}
	}
}

func TestGrepRejectsInvalidRegexpAndOutsidePath(t *testing.T) {
	root := t.TempDir()
	if _, err := (Grep{Root: root}).Run(context.Background(), map[string]any{"pattern": "["}); err == nil {
		t.Fatal("expected invalid regex error")
	}
	outside := filepath.Join(t.TempDir(), "x.txt")
	writeSearchFile(t, outside, "x")
	if _, err := (Grep{Root: root}).Run(context.Background(), map[string]any{"pattern": "x", "path": outside}); err == nil {
		t.Fatal("expected outside path error")
	}
}

func TestGlobSupportsDoubleStarAndScopesToRoot(t *testing.T) {
	root := t.TempDir()
	writeSearchFile(t, filepath.Join(root, "a.go"), "package a")
	writeSearchFile(t, filepath.Join(root, "nested", "b.go"), "package b")
	writeSearchFile(t, filepath.Join(root, "nested", "c.txt"), "c")
	out, err := (Glob{Root: root}).Run(context.Background(), map[string]any{"pattern": "**/*.go"})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, want := range []string{"a.go", "nested/b.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("glob output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "c.txt") {
		t.Fatalf("glob output included non-match:\n%s", out)
	}

	outside := filepath.Join(t.TempDir(), "x")
	if _, err := (Glob{Root: root}).Run(context.Background(), map[string]any{"pattern": "*", "path": outside}); err == nil {
		t.Fatal("expected outside path error")
	}
}

func writeSearchFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
