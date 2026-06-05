package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAncestors_StopsAtRepoRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	cwd := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	got := Ancestors(cwd)
	// cwd-first, up to and including the repo root.
	want := []string{cwd, filepath.Join(root, "a"), root}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Ancestors[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRoot_FindsRepoRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	cwd := filepath.Join(root, "pkg")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Root(cwd); got != root {
		t.Fatalf("Root = %q, want %q", got, root)
	}
}

func TestRoot_FallsBackToCwdWhenNoRepo(t *testing.T) {
	dir := t.TempDir() // no .git anywhere up to it (temp dirs aren't in a repo)
	got := Root(dir)
	abs, _ := filepath.Abs(dir)
	if got != abs {
		t.Fatalf("Root = %q, want cwd %q", got, abs)
	}
}

func TestResolveWithinRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveWithin(root, filepath.Join(root, "link", "secret.txt")); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestResolveWithinRejectsNewFileUnderSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveWithin(root, filepath.Join(root, "link", "new.txt")); err == nil {
		t.Fatal("expected new file under symlink escape to be rejected")
	}
}

func TestResolveWithinAllowsMissingPathUnderRoot(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveWithin(root, filepath.Join(root, "new", "file.txt"))
	if err != nil {
		t.Fatalf("ResolveWithin: %v", err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(realRoot, "new", "file.txt")
	if got != want {
		t.Fatalf("ResolveWithin = %q, want %q", got, want)
	}
}
