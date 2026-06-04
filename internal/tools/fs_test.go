package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if out != "hi\nthere\n" {
		t.Fatalf("got %q", out)
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
	if out != "b\nc" {
		t.Fatalf("got %q, want %q", out, "b\nc")
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
	// Without pagination → falls into the line-paging branch; the single line
	// exceeds MaxReadBytes, so this should error rather than dump megabytes.
	_, err := ReadFile{}.Run(context.Background(), map[string]any{"path": path})
	if err == nil {
		t.Fatal("expected error for oversized read without pagination")
	}
	if !strings.Contains(err.Error(), "exceeds") {
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
