package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

func TestGrepFindsMatchBeyondPreviousLineLimit(t *testing.T) {
	root := t.TempDir()
	longPrefix := strings.Repeat("x", MaxReadBytes+1024)
	writeSearchFile(t, filepath.Join(root, "long.txt"), longPrefix+"needle\n")

	out, err := (Grep{Root: root}).Run(context.Background(), map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatalf("grep long line: %v", err)
	}
	got := decodeGrepResult(t, out)
	if got.Count != 1 || len(got.Matches) != 1 {
		t.Fatalf("count/matches = %d/%d, want 1/1: %#v", got.Count, len(got.Matches), got)
	}
	match := got.Matches[0]
	if match.Path != "long.txt" || match.Line != 1 || !strings.Contains(match.Text, "needle") {
		t.Fatalf("match = %#v, want bounded line 1 excerpt containing needle", match)
	}
	if len(match.Text) > maxGrepTextBytes {
		t.Fatalf("match text length = %d, want at most %d", len(match.Text), maxGrepTextBytes)
	}
}

func TestGrepRejectsOversizedLinesWithoutUnboundedBuffering(t *testing.T) {
	root := t.TempDir()
	writeSearchFile(t, filepath.Join(root, "oversized.txt"), strings.Repeat("x", maxGrepLineBytes+1))

	_, err := (Grep{Root: root}).Run(context.Background(), map[string]any{"pattern": "needle"})
	if err == nil {
		t.Fatal("expected grep to reject an oversized line")
	}
	for _, want := range []string{"oversized.txt", "line 1", fmt.Sprint(maxGrepLineBytes)} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
}

func TestGrepBoundsLongContextLines(t *testing.T) {
	root := t.TempDir()
	writeSearchFile(t, filepath.Join(root, "context.txt"), strings.Repeat("x", MaxReadBytes+1024)+"\nneedle\n")

	out, err := (Grep{Root: root}).Run(context.Background(), map[string]any{
		"pattern":       "needle",
		"context_lines": 1.0,
	})
	if err != nil {
		t.Fatalf("grep long context: %v", err)
	}
	got := decodeGrepResult(t, out)
	if got.Count != 1 || len(got.Matches[0].ContextBefore) != 1 {
		t.Fatalf("grep result = %#v, want one match with one context line", got)
	}
	if contextText := got.Matches[0].ContextBefore[0].Text; len(contextText) > maxGrepTextBytes {
		t.Fatalf("context text length = %d, want at most %d", len(contextText), maxGrepTextBytes)
	}
}

func TestGrepSurfacesFileReadFailures(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "blocked.txt")
	writeSearchFile(t, path, "needle\n")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(path); err == nil {
		f.Close()
		t.Skip("current user can read files regardless of their mode")
	}

	_, err := (Grep{Root: root}).Run(context.Background(), map[string]any{
		"pattern": "absent",
		"path":    "blocked.txt",
	})
	if err == nil {
		t.Fatal("expected grep to surface the file read failure")
	}
	if !strings.Contains(err.Error(), "blocked.txt") {
		t.Fatalf("error = %q, want failed path", err)
	}
}

func TestFilesUnderPropagatesRecursiveTraversalFailures(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	writeSearchFile(t, filepath.Join(blocked, "needle.txt"), "needle\n")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if entries, err := os.ReadDir(blocked); err == nil {
		_ = entries
		t.Skip("current user can traverse directories regardless of their mode")
	}

	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	searchRoot, err := os.OpenRoot(canonicalRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer searchRoot.Close()

	_, err = filesUnder(context.Background(), searchRoot, ".")
	if err == nil {
		t.Fatal("expected recursive traversal failure")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("error = %q, want failed directory path", err)
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

func TestGrepRejectsDiscoveredSymlinkOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	const sentinel = "outside sentinel must never be returned by grep"
	writeSearchFile(t, filepath.Join(root, "safe.txt"), "safe contents")
	writeSearchFile(t, outside, sentinel)
	escape, err := filepath.Rel(root, outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escape, filepath.Join(root, "leak.txt")); err != nil {
		t.Fatal(err)
	}

	out, err := (Grep{Root: root}).Run(context.Background(), map[string]any{
		"pattern": "sentinel",
	})
	if err == nil {
		t.Fatal("expected grep to reject a discovered symlink outside the workspace")
	}
	if !strings.Contains(err.Error(), "leak.txt") {
		t.Fatalf("error = %q, want escaping symlink path", err)
	}
	if strings.Contains(out, sentinel) {
		t.Fatalf("grep returned outside sentinel: %q", out)
	}
}

func TestGrepAllowsDiscoveredSymlinkWithinWorkspace(t *testing.T) {
	root := t.TempDir()
	writeSearchFile(t, filepath.Join(root, "data", "safe.txt"), "internal needle")
	if err := os.Symlink(filepath.Join("data", "safe.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}

	out, err := (Grep{Root: root}).Run(context.Background(), map[string]any{
		"pattern": "needle",
	})
	if err != nil {
		t.Fatalf("grep internal symlink: %v", err)
	}
	got := decodeGrepResult(t, out)
	if got.Count != 2 {
		t.Fatalf("grep result = %#v, want target and internal symlink matches", got)
	}
	paths := []string{got.Matches[0].Path, got.Matches[1].Path}
	if want := []string{"data/safe.txt", "link.txt"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("match paths = %v, want %v", paths, want)
	}
}

func TestGrepAllowsDiscoveredAbsoluteSymlinkWithinWorkspace(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "data", "safe.txt")
	writeSearchFile(t, target, "internal needle")
	if err := os.Symlink(target, filepath.Join(root, "absolute-link.txt")); err != nil {
		t.Fatal(err)
	}

	out, err := (Grep{Root: root}).Run(context.Background(), map[string]any{
		"pattern": "needle",
	})
	if err != nil {
		t.Fatalf("grep absolute internal symlink: %v", err)
	}
	got := decodeGrepResult(t, out)
	if got.Count != 2 {
		t.Fatalf("grep result = %#v, want target and absolute internal symlink matches", got)
	}
	paths := []string{got.Matches[0].Path, got.Matches[1].Path}
	if want := []string{"absolute-link.txt", "data/safe.txt"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("match paths = %v, want %v", paths, want)
	}
}

func TestRootedGrepReadUsesResolvedTargetAfterLinkSwap(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "safe.txt")
	writeSearchFile(t, target, "safe contents")
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	searchRoot, err := os.OpenRoot(canonicalRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer searchRoot.Close()
	files, err := filesUnder(context.Background(), searchRoot, ".")
	if err != nil {
		t.Fatalf("discover files: %v", err)
	}
	var discovered grepFile
	for _, file := range files {
		if file.path == "link.txt" {
			discovered = file
			break
		}
	}
	if discovered.path == "" || discovered.openPath != "safe.txt" {
		t.Fatalf("discovered link = %#v, want display link.txt opened as safe.txt", discovered)
	}

	outside := filepath.Join(t.TempDir(), "secret.txt")
	const sentinel = "outside sentinel must never win a link swap"
	writeSearchFile(t, outside, sentinel)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	escape, err := filepath.Rel(root, outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escape, link); err != nil {
		t.Fatal(err)
	}

	lines, err := readTextLines(context.Background(), searchRoot, discovered.openPath)
	if err != nil {
		t.Fatalf("read resolved target: %v", err)
	}
	if got := strings.Join(lines, "\n"); got != "safe contents" || strings.Contains(got, sentinel) {
		t.Fatalf("rooted read = %q, want safe resolved target", got)
	}
}

func TestRootedGrepReadRejectsFileSwappedToOutsideSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	const sentinel = "outside sentinel must never win a symlink race"
	writeSearchFile(t, outside, sentinel)
	victim := filepath.Join(root, "victim.txt")
	writeSearchFile(t, victim, "safe contents")

	searchRoot, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer searchRoot.Close()
	files, err := filesUnder(context.Background(), searchRoot, ".")
	if err != nil {
		t.Fatalf("discover files: %v", err)
	}
	if want := []grepFile{{path: "victim.txt", openPath: "victim.txt"}}; !reflect.DeepEqual(files, want) {
		t.Fatalf("discovered files = %v, want %v", files, want)
	}

	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}
	escape, err := filepath.Rel(root, outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escape, victim); err != nil {
		t.Fatal(err)
	}
	lines, err := readTextLines(context.Background(), searchRoot, files[0].openPath)
	if err == nil {
		t.Fatal("expected rooted read to reject the raced symlink")
	}
	if strings.Contains(strings.Join(lines, "\n"), sentinel) {
		t.Fatalf("rooted read returned outside sentinel: %q", lines)
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

func TestGrepFileDiscoveryHonorsCancelledContext(t *testing.T) {
	root := t.TempDir()
	writeSearchFile(t, filepath.Join(root, "a.txt"), "needle")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := (Grep{Root: root}).Run(ctx, map[string]any{"pattern": "needle"})
	if err != context.Canceled {
		t.Fatalf("error = %v, want context canceled", err)
	}
}
