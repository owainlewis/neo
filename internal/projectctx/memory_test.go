package projectctx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadMemory_MissingReturnsAbsent(t *testing.T) {
	root := t.TempDir()

	doc, ok, err := LoadMemory(root)
	if err != nil {
		t.Fatalf("load memory: %v", err)
	}
	if ok {
		t.Fatalf("expected no memory doc, got %+v", doc)
	}
}

func TestLoadMemory_ReadsProjectRootMemory(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "memory.md"), "# Project memory\n\n- prefers Go 1.26")

	doc, ok, err := LoadMemory(root)
	if err != nil {
		t.Fatalf("load memory: %v", err)
	}
	if !ok {
		t.Fatal("expected memory doc")
	}
	if doc.Path != filepath.Join(root, "memory.md") {
		t.Fatalf("path = %q", doc.Path)
	}
	if !strings.Contains(doc.Content, "prefers Go 1.26") {
		t.Fatalf("unexpected content: %q", doc.Content)
	}
}

func TestMemorySection_RendersDistinctLabel(t *testing.T) {
	got := MemorySection(Doc{Path: "/repo/memory.md", Content: "- ship docs check"})

	for _, want := range []string{
		"# Project memory",
		"memory.md in this project",
		"## /repo/memory.md",
		"- ship docs check",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("memory section missing %q\n---\n%s", want, got)
		}
	}
}

func TestAppendMemory_CreatesFileWithHeadingAndEntry(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	path, err := AppendMemory(root, "prefer table-driven tests", now)
	if err != nil {
		t.Fatalf("append memory: %v", err)
	}
	if path != filepath.Join(root, "memory.md") {
		t.Fatalf("path = %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read memory: %v", err)
	}
	want := "# Project memory\n\n- 2026-06-10: prefer table-driven tests\n"
	if string(got) != want {
		t.Fatalf("memory contents = %q, want %q", string(got), want)
	}
}

func TestAppendMemory_AppendsWithoutOverwriting(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "memory.md"), "# Project memory\n\n- 2026-06-09: use Go\n")

	_, err := AppendMemory(root, "prefer small verified changes", time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("append memory: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "memory.md"))
	if err != nil {
		t.Fatalf("read memory: %v", err)
	}
	text := string(got)
	for _, want := range []string{
		"- 2026-06-09: use Go",
		"- 2026-06-10: prefer small verified changes",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("memory contents missing %q\n---\n%s", want, text)
		}
	}
}

func TestAppendMemory_RejectsBlankEntry(t *testing.T) {
	root := t.TempDir()

	if _, err := AppendMemory(root, "   ", time.Now()); err == nil {
		t.Fatal("expected blank memory to fail")
	}
}

func TestAppendMemory_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "memory.md")
	write(t, target, "# External memory\n")
	if err := os.Symlink(target, filepath.Join(root, "memory.md")); err != nil {
		t.Fatal(err)
	}

	if _, err := AppendMemory(root, "do not escape", time.Now()); err == nil {
		t.Fatal("expected symlink escape to fail")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read outside target: %v", err)
	}
	if strings.Contains(string(got), "do not escape") {
		t.Fatalf("outside memory target was modified:\n%s", got)
	}
}
