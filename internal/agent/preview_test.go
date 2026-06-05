package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewWriteFileShowsDiffBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	preview := Preview("write_file", map[string]any{"path": path, "content": "new\n"})
	for _, want := range []string{"--- " + path, "+++ " + path, "-old", "+new"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q:\n%s", want, preview)
		}
	}
	got, _ := os.ReadFile(path)
	if string(got) != "old\n" {
		t.Fatalf("preview mutated file: %q", got)
	}
}

func TestPreviewEditFileShowsDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("alpha beta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	preview := Preview("edit_file", map[string]any{
		"path":       path,
		"old_string": "beta",
		"new_string": "gamma",
	})
	if !strings.Contains(preview, "-alpha beta") || !strings.Contains(preview, "+alpha gamma") {
		t.Fatalf("unexpected preview:\n%s", preview)
	}
}
