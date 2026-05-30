package projectctx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo lays out a temp directory tree that looks like a git repo rooted at
// root, with a nested working directory, and points HOME at an isolated dir so
// the user-global lookup is deterministic.
func repo(t *testing.T) (root, cwd, home string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "repo")
	cwd = filepath.Join(root, "pkg", "sub")
	home = filepath.Join(base, "home")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return root, cwd, home
}

func TestLoad_NoFilesReturnsEmpty(t *testing.T) {
	_, cwd, _ := repo(t)
	docs, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected no docs, got %d: %v", len(docs), docs)
	}
}

func TestLoad_OrdersGlobalThenOutermostToCwd(t *testing.T) {
	root, cwd, home := repo(t)
	write(t, filepath.Join(home, ".neo", "AGENTS.md"), "global rules")
	write(t, filepath.Join(root, "AGENTS.md"), "repo rules")
	write(t, filepath.Join(cwd, "AGENTS.md"), "local rules")

	docs, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected 3 docs, got %d: %v", len(docs), docs)
	}
	want := []string{"global rules", "repo rules", "local rules"}
	for i, w := range want {
		if docs[i].Content != w {
			t.Errorf("doc[%d] = %q, want %q", i, docs[i].Content, w)
		}
	}
}

func TestLoad_StopsAtRepoRoot(t *testing.T) {
	root, cwd, _ := repo(t)
	// An AGENTS.md above the repo root must NOT be picked up.
	write(t, filepath.Join(filepath.Dir(root), "AGENTS.md"), "outside repo")
	write(t, filepath.Join(root, "AGENTS.md"), "repo rules")

	docs, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(docs) != 1 || docs[0].Content != "repo rules" {
		t.Fatalf("expected only repo rules, got %v", docs)
	}
}

func TestLoad_SkipsEmptyFiles(t *testing.T) {
	root, cwd, _ := repo(t)
	write(t, filepath.Join(root, "AGENTS.md"), "   \n\t\n")

	docs, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected empty file skipped, got %v", docs)
	}
}

func TestAugment_NoDocsReturnsBaseUnchanged(t *testing.T) {
	base := "you are neo"
	if got := Augment(base, nil); got != base {
		t.Fatalf("expected base unchanged, got %q", got)
	}
}

func TestAugment_AppendsLabelledSections(t *testing.T) {
	base := "you are neo"
	docs := []Doc{
		{Path: "/repo/AGENTS.md", Content: "use tabs"},
		{Path: "/repo/pkg/AGENTS.md", Content: "no globals"},
	}
	got := Augment(base, docs)

	if !strings.HasPrefix(got, base) {
		t.Errorf("augmented prompt must start with base")
	}
	for _, want := range []string{
		"# Project instructions",
		"## /repo/AGENTS.md",
		"use tabs",
		"## /repo/pkg/AGENTS.md",
		"no globals",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("augmented prompt missing %q\n---\n%s", want, got)
		}
	}
	// More-specific section must appear after the more-general one.
	if strings.Index(got, "/repo/pkg/AGENTS.md") < strings.Index(got, "/repo/AGENTS.md") {
		t.Errorf("expected pkg section to follow repo-root section")
	}
}
