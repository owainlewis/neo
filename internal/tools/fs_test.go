package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadFile_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hi\nthere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if want := "     1\thi\n     2\tthere\n"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestReadFile_OffsetLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lines.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFile{}.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 2.0, // JSON numbers arrive as float64
		"limit":  2.0,
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if want := "     2\tb\n     3\tc"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestReadFile_LargePaginatedRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large-lines.txt")

	var content strings.Builder
	for i := 1; content.Len() <= MaxReadBytes+1024; i++ {
		fmt.Fprintf(&content, "line-%05d\n", i)
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := ReadFile{}.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 250,
		"limit":  3,
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "   250\tline-00250\n   251\tline-00251\n   252\tline-00252"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestReadFile_OffsetPastEOF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "short.txt")
	if err := os.WriteFile(path, []byte("a\nb"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadFile{}.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 10,
		"limit":  2,
	})
	if err == nil {
		t.Fatal("expected error for offset past EOF")
	}
	if !strings.Contains(err.Error(), "offset 10 is past end of file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadFile_EmptyFileOffsetOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := ReadFile{}.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 1,
		"limit":  1,
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if out != "" {
		t.Fatalf("got %q, want empty string", out)
	}
}

func TestReadFile_OversizedNeedsPagination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	big := make([]byte, MaxReadBytes+1024)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path})
	if err == nil {
		t.Fatal("expected error for oversized read without pagination")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadFile_PaginatedSelectionExceedsCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long-line.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", MaxReadBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadFile{}.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 1,
		"limit":  1,
	})
	if err == nil {
		t.Fatal("expected error for oversized paginated selection")
	}
	if !strings.Contains(err.Error(), "selection exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWriteFile_CreatesAndIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "out.txt")
	_, err := WriteFile{}.Run(context.Background(), map[string]any{
		"path":    path,
		"content": "hello",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("got %q", string(b))
	}
	if got := fileMode(t, path); got != 0o644 {
		t.Fatalf("mode = %v, want %v", got, os.FileMode(0o644))
	}
	// No leftover temp files in the target directory.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".neo-write-") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func TestWriteFile_PreservesExistingMode(t *testing.T) {
	tests := []struct {
		name string
		mode os.FileMode
	}{
		{name: "executable", mode: 0o755},
		{name: "private", mode: 0o600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "f.txt")
			writeFileWithMode(t, path, []byte("old"), tt.mode)

			if _, err := (WriteFile{}).Run(context.Background(), map[string]any{
				"path":    path,
				"content": "new",
			}); err != nil {
				t.Fatalf("write: %v", err)
			}

			if got := fileMode(t, path); got != tt.mode {
				t.Fatalf("mode = %v, want %v", got, tt.mode)
			}
		})
	}
}

func TestEditFile_UniqueMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("alpha bravo charlie"), 0o644)

	if _, err := (EditFile{}).Run(context.Background(), map[string]any{
		"path":       path,
		"old_string": "bravo",
		"new_string": "BRAVO",
	}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "alpha BRAVO charlie" {
		t.Fatalf("got %q", string(b))
	}
}

func TestEditFile_PreservesExistingMode(t *testing.T) {
	tests := []struct {
		name string
		mode os.FileMode
	}{
		{name: "executable", mode: 0o755},
		{name: "private", mode: 0o600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "f.txt")
			writeFileWithMode(t, path, []byte("alpha bravo charlie"), tt.mode)

			if _, err := (EditFile{}).Run(context.Background(), map[string]any{
				"path":       path,
				"old_string": "bravo",
				"new_string": "BRAVO",
			}); err != nil {
				t.Fatalf("edit: %v", err)
			}

			if got := fileMode(t, path); got != tt.mode {
				t.Fatalf("mode = %v, want %v", got, tt.mode)
			}
		})
	}
}

func TestEditFile_AmbiguousMatchFailsAndDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	original := "foo foo foo"
	os.WriteFile(path, []byte(original), 0o644)

	_, err := EditFile{}.Run(context.Background(), map[string]any{
		"path":       path,
		"old_string": "foo",
		"new_string": "bar",
	})
	if err == nil {
		t.Fatal("expected error on ambiguous match")
	}
	b, _ := os.ReadFile(path)
	if string(b) != original {
		t.Fatalf("file mutated on failed edit: %q", string(b))
	}
}

func TestEditFile_MissingMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("nothing here"), 0o644)

	_, err := EditFile{}.Run(context.Background(), map[string]any{
		"path":       path,
		"old_string": "missing",
		"new_string": "x",
	})
	if err == nil {
		t.Fatal("expected error when old_string is absent")
	}
	if !strings.Contains(err.Error(), "read the file and retry with exact text") {
		t.Fatalf("error is not actionable: %v", err)
	}
}

func TestEditFile_EmptyMatchFailsAndDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	original := "leave me alone"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := EditFile{}.Run(context.Background(), map[string]any{
		"path":       path,
		"old_string": "",
		"new_string": "surprise",
	})
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("expected actionable empty match error, got %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != original {
		t.Fatalf("file mutated on failed edit: %q", b)
	}
}

func writeFileWithMode(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestReadFile_NumbersMatchGrep(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(path, []byte("alpha\nbravo\nneedle\ndelta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	read, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	raw, err := Grep{Root: dir}.Run(context.Background(), map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	var got grepResult
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode grep: %v", err)
	}
	if len(got.Matches) != 1 {
		t.Fatalf("grep matches = %d, want 1", len(got.Matches))
	}
	if want := lineNumberPrefix(got.Matches[0].Line) + "needle"; !strings.Contains(read, want) {
		t.Fatalf("read_file line %d is not numbered to match grep:\n%s", got.Matches[0].Line, read)
	}
}

func TestEditFile_RejectsExternalChangeSinceRead(t *testing.T) {
	files := NewFileTools()
	read, write, edit := files[0].(ReadFile), files[1].(WriteFile), files[2].(EditFile)
	ctx := context.Background()

	path := filepath.Join(t.TempDir(), "code.go")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := read.Run(ctx, map[string]any{"path": path}); err != nil {
		t.Fatalf("read: %v", err)
	}
	// Something outside the agent rewrites the file. The unique-match check
	// would still pass, so only the stamp catches this.
	if err := os.WriteFile(path, []byte("original\nadded by the user\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touch(t, path)

	_, err := edit.Run(ctx, map[string]any{"path": path, "old_string": "original", "new_string": "edited"})
	if err == nil {
		t.Fatal("expected the stale edit to be rejected")
	}
	if !strings.Contains(err.Error(), "changed since you read it") {
		t.Fatalf("error does not tell the agent what to do: %v", err)
	}

	// Re-reading clears it.
	if _, err := read.Run(ctx, map[string]any{"path": path}); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if _, err := edit.Run(ctx, map[string]any{"path": path, "old_string": "original", "new_string": "edited"}); err != nil {
		t.Fatalf("edit after re-read: %v", err)
	}

	// The agent's own edits and writes are not external changes.
	if _, err := edit.Run(ctx, map[string]any{"path": path, "old_string": "edited", "new_string": "again"}); err != nil {
		t.Fatalf("consecutive edit: %v", err)
	}
	if _, err := write.Run(ctx, map[string]any{"path": path, "content": "replaced\n"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := edit.Run(ctx, map[string]any{"path": path, "old_string": "replaced", "new_string": "final"}); err != nil {
		t.Fatalf("edit after write: %v", err)
	}
}

func TestEditFile_UnreadFileIsNotStale(t *testing.T) {
	files := NewFileTools()
	edit := files[2].(EditFile)

	path := filepath.Join(t.TempDir(), "never-read.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := edit.Run(context.Background(), map[string]any{"path": path, "old_string": "hello", "new_string": "bye"}); err != nil {
		t.Fatalf("editing a file the agent never read must work: %v", err)
	}
}

func TestFileState_IsPerAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.txt")
	if err := os.WriteFile(path, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	coordinator, subagent := NewFileTools(), NewFileTools()
	if _, err := subagent[0].(ReadFile).Run(context.Background(), map[string]any{"path": path}); err != nil {
		t.Fatalf("subagent read: %v", err)
	}
	if coordinator[2].(EditFile).State.changedSinceRead(path) {
		t.Fatal("a subagent's read must not register as the coordinator having read the file")
	}
}

// touch advances the modification time past the filesystem's timestamp
// granularity so the change is observable regardless of how fast the test runs.
func touch(t *testing.T, path string) {
	t.Helper()
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
}

func TestReadFile_CancelledContextBeforeOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (ReadFile{}).Run(ctx, map[string]any{"path": path}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// A missing path must still report cancellation, not the filesystem error.
	if _, err := (ReadFile{}).Run(ctx, map[string]any{"path": filepath.Join(t.TempDir(), "absent")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
