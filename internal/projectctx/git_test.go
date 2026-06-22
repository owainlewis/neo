package projectctx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadGitContext_ReadsBranchStatusAndLog(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.name", "Neo Test")
	runGit(t, root, "config", "user.email", "neo@example.com")
	cwd := filepath.Join(root, "pkg")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(cwd, "tracked.txt"), "first\n")
	runGit(t, root, "add", "pkg/tracked.txt")
	runGit(t, root, "commit", "-m", "seed commit")

	write(t, filepath.Join(cwd, "tracked.txt"), "updated\n")
	write(t, filepath.Join(cwd, "untracked.txt"), "new\n")

	doc, ok := LoadGitContext(cwd)
	if !ok {
		t.Fatal("expected git context")
	}
	for _, want := range []string{
		"Branch: main",
		"git status --short",
		"M tracked.txt",
		"?? untracked.txt",
		"git log --oneline -5",
		"seed commit",
	} {
		if !strings.Contains(doc.Content, want) {
			t.Fatalf("git context missing %q\n---\n%s", want, doc.Content)
		}
	}
}

func TestLoadGitContext_OutsideRepoReturnsAbsent(t *testing.T) {
	if _, ok := LoadGitContext(t.TempDir()); ok {
		t.Fatal("expected no git context outside a repo")
	}
}

func TestGitSection_RendersDistinctLabel(t *testing.T) {
	got := GitSection(Doc{Content: "Branch: main\n\ngit status --short\nM tracked.txt\n\ngit log --oneline -5\nabc123 seed commit"})

	for _, want := range []string{
		"# Git context",
		"lightweight snapshot captured at session start",
		"## Repository state",
		"Branch: main",
		"git status --short",
		"git log --oneline -5",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("git section missing %q\n---\n%s", want, got)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
