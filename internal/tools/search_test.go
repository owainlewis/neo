package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

func TestGrepReturnsJSONWithContextAndTruncation(t *testing.T) {
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
	got := decodeGrepResult(t, out)
	if !got.Truncated {
		t.Fatalf("expected truncated result, got %#v", got)
	}
	if got.Count != 1 || len(got.Matches) != 1 {
		t.Fatalf("count/matches = %d/%d, want 1/1: %#v", got.Count, len(got.Matches), got)
	}
	match := got.Matches[0]
	if match.Path != "a.txt" || match.Line != 2 || match.Text != "needle one" {
		t.Fatalf("match = %#v, want a.txt line 2 needle one", match)
	}
	if want := []grepContextLine{{Line: 1, Text: "before"}}; !reflect.DeepEqual(match.ContextBefore, want) {
		t.Fatalf("context_before = %#v, want %#v", match.ContextBefore, want)
	}
	if want := []grepContextLine{{Line: 3, Text: "after"}}; !reflect.DeepEqual(match.ContextAfter, want) {
		t.Fatalf("context_after = %#v, want %#v", match.ContextAfter, want)
	}
}

func TestGrepReturnsJSONForNoMatches(t *testing.T) {
	root := t.TempDir()
	writeSearchFile(t, filepath.Join(root, "a.txt"), "haystack\n")
	out, err := (Grep{Root: root}).Run(context.Background(), map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	got := decodeGrepResult(t, out)
	if got.Count != 0 || got.Truncated || len(got.Matches) != 0 {
		t.Fatalf("grep no-match result = %#v, want empty non-truncated JSON", got)
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
	got := decodeGlobResult(t, out)
	if got.Truncated {
		t.Fatalf("expected non-truncated glob result, got %#v", got)
	}
	if got.Count != 2 || !reflect.DeepEqual(got.Matches, []string{"a.go", "nested/b.go"}) {
		t.Fatalf("glob result = %#v, want two go files", got)
	}

	outside := filepath.Join(t.TempDir(), "x")
	if _, err := (Glob{Root: root}).Run(context.Background(), map[string]any{"pattern": "*", "path": outside}); err == nil {
		t.Fatal("expected outside path error")
	}
}

func TestGlobReturnsJSONForNoMatchesAndTruncation(t *testing.T) {
	root := t.TempDir()
	writeSearchFile(t, filepath.Join(root, "a.txt"), "a")
	writeSearchFile(t, filepath.Join(root, "b.txt"), "b")

	out, err := (Glob{Root: root}).Run(context.Background(), map[string]any{"pattern": "*.go"})
	if err != nil {
		t.Fatalf("glob no matches: %v", err)
	}
	empty := decodeGlobResult(t, out)
	if empty.Count != 0 || empty.Truncated || len(empty.Matches) != 0 {
		t.Fatalf("glob no-match result = %#v, want empty non-truncated JSON", empty)
	}

	out, err = (Glob{Root: root}).Run(context.Background(), map[string]any{
		"pattern":     "*.txt",
		"max_matches": 1.0,
	})
	if err != nil {
		t.Fatalf("glob truncated: %v", err)
	}
	truncated := decodeGlobResult(t, out)
	if truncated.Count != 1 || !truncated.Truncated || !reflect.DeepEqual(truncated.Matches, []string{"a.txt"}) {
		t.Fatalf("glob truncated result = %#v, want one returned match and truncated=true", truncated)
	}
}

func TestSearchToolsRejectSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeSearchFile(t, filepath.Join(outside, "secret.txt"), "needle")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := (Grep{Root: root}).Run(context.Background(), map[string]any{
		"pattern": "needle",
		"path":    filepath.Join(root, "link", "secret.txt"),
	}); err == nil {
		t.Fatal("expected grep symlink escape error")
	}
	if _, err := (Glob{Root: root}).Run(context.Background(), map[string]any{
		"pattern": "*",
		"path":    filepath.Join(root, "link"),
	}); err == nil {
		t.Fatal("expected glob symlink escape error")
	}
}

func decodeGrepResult(t *testing.T, out string) grepResult {
	t.Helper()
	var result grepResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("grep output is not valid JSON: %v\n%s", err, out)
	}
	return result
}

func decodeGlobResult(t *testing.T, out string) globResult {
	t.Helper()
	var result globResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("glob output is not valid JSON: %v\n%s", err, out)
	}
	return result
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
