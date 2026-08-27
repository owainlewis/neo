package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireRipgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath(SearchBinary); err != nil {
		t.Skipf("%s is not installed", SearchBinary)
	}
}

// searchWorkspace builds a small git repo with an ignored directory, so tests
// can assert that discovery honours .gitignore.
func searchWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "build/\n")
	write("main.go", "package main\n\nfunc needle() {}\n")
	write("pkg/helper.go", "package pkg\n\n// needle lives here too\n")
	write("build/generated.go", "package build\n\nfunc needle() {}\n")
	return root
}

func TestGrep_FindsMatchesWithLineNumbers(t *testing.T) {
	requireRipgrep(t)
	root := searchWorkspace(t)

	out, err := Grep{Root: root}.Run(context.Background(), map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	for _, want := range []string{"main.go:3:", "pkg/helper.go:3:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestGrep_HonoursGitignore(t *testing.T) {
	requireRipgrep(t)
	root := searchWorkspace(t)

	out, err := Grep{Root: root}.Run(context.Background(), map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if strings.Contains(out, "build/generated.go") {
		t.Fatalf("ignored directory was searched:\n%s", out)
	}
}

func TestGrep_NoMatchesIsNotAnError(t *testing.T) {
	requireRipgrep(t)

	out, err := Grep{Root: searchWorkspace(t)}.Run(context.Background(), map[string]any{"pattern": "haystack"})
	if err != nil {
		t.Fatalf("a pattern that matches nothing is a result, not a failure: %v", err)
	}
	if out != "no matches" {
		t.Fatalf("out = %q, want %q", out, "no matches")
	}
}

func TestGrep_InvalidPatternIsAnError(t *testing.T) {
	requireRipgrep(t)

	if _, err := (Grep{Root: searchWorkspace(t)}).Run(context.Background(), map[string]any{"pattern": "("}); err == nil {
		t.Fatal("expected an error for an unparseable pattern")
	}
}

func TestGrep_ContextLines(t *testing.T) {
	requireRipgrep(t)
	root := searchWorkspace(t)

	out, err := Grep{Root: root}.Run(context.Background(), map[string]any{
		"pattern":       "needle",
		"path":          "main.go",
		"context_lines": 2,
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(out, "package main") {
		t.Fatalf("context lines were not returned:\n%s", out)
	}
}

func TestGrep_TruncatesAtMaxMatches(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	var body strings.Builder
	for i := range 50 {
		fmt.Fprintf(&body, "needle %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "many.txt"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Grep{Root: root}.Run(context.Background(), map[string]any{"pattern": "needle", "max_matches": 5})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if got := strings.Count(out, "many.txt:"); got != 5 {
		t.Fatalf("returned %d matches, want 5:\n%s", got, out)
	}
	if !strings.Contains(out, "showing the first 5 of 50") {
		t.Fatalf("truncation was not reported:\n%s", out)
	}
}

func TestGlob_MatchesRecursivelyAndHonoursGitignore(t *testing.T) {
	requireRipgrep(t)
	root := searchWorkspace(t)

	out, err := Glob{Root: root}.Run(context.Background(), map[string]any{"pattern": "**/*.go"})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, want := range []string{"main.go", "pkg/helper.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "build/generated.go") {
		t.Fatalf("ignored directory was listed:\n%s", out)
	}
}

func TestGlob_NoMatches(t *testing.T) {
	requireRipgrep(t)

	out, err := Glob{Root: searchWorkspace(t)}.Run(context.Background(), map[string]any{"pattern": "**/*.rs"})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if out != "no matches" {
		t.Fatalf("out = %q, want %q", out, "no matches")
	}
}

func TestSearch_CancelledContext(t *testing.T) {
	requireRipgrep(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (Grep{Root: searchWorkspace(t)}).Run(ctx, map[string]any{"pattern": "needle"}); err == nil {
		t.Fatal("expected a cancelled search to fail")
	}
}

func TestSearch_MissingPattern(t *testing.T) {
	for _, tool := range []Tool{Grep{}, Glob{}} {
		if _, err := tool.Run(context.Background(), map[string]any{}); err == nil {
			t.Fatalf("%s: expected an error for a missing pattern", tool.Name())
		}
	}
}

func TestSearchToolsAreParallelSafe(t *testing.T) {
	registry := NewRegistry(Grep{}, Glob{}, Bash{})
	for _, name := range []string{"grep", "glob"} {
		if !registry.ParallelSafe(name, nil) {
			t.Fatalf("%s must stay parallel-safe; it is why these are not folded into bash", name)
		}
	}
	if registry.ParallelSafe("bash", nil) {
		t.Fatal("bash must remain serial")
	}
}
