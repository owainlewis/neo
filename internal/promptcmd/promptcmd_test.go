package promptcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func commandRepo(t *testing.T) (root, cwd, home string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "repo")
	cwd = filepath.Join(root, "pkg")
	home = filepath.Join(base, "home")
	for _, d := range []string{filepath.Join(root, ".git"), cwd, home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	return root, cwd, home
}

func writeCommand(t *testing.T, base, name, body string) {
	t.Helper()
	dir := filepath.Join(base, ".neo", "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProjectOverridesGlobal(t *testing.T) {
	root, cwd, home := commandRepo(t)
	writeCommand(t, home, "review", "---\ndescription: global\n---\nglobal body")
	writeCommand(t, root, "review", "---\ndescription: project\n---\nproject body")

	got, warnings := Load(cwd)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if len(got) != 1 {
		t.Fatalf("commands = %+v", got)
	}
	if got[0].Name != "review" || got[0].Description != "project" || got[0].Body != "project body" {
		t.Fatalf("unexpected command: %+v", got[0])
	}
}

func TestLoadSkipsEmptyAndWarnsMalformed(t *testing.T) {
	root, cwd, _ := commandRepo(t)
	writeCommand(t, root, "empty", "")
	writeCommand(t, root, "bad", "---\nname: [oops\n")

	got, warnings := Load(cwd)
	if len(got) != 0 {
		t.Fatalf("expected no commands, got %+v", got)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0].Error(), "frontmatter") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestLoadDescriptionFallsBackToFirstBodyLine(t *testing.T) {
	root, cwd, _ := commandRepo(t)
	writeCommand(t, root, "commit", "# Commit helper\n\nWrite a commit message.")

	got, warnings := Load(cwd)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if len(got) != 1 || got[0].Description != "Commit helper" {
		t.Fatalf("commands = %+v", got)
	}
}

func TestExpandUsesArgumentsPlaceholderOrAppendsArguments(t *testing.T) {
	withPlaceholder := Expand(Command{Body: "Review $ARGUMENTS carefully."}, "diff")
	if withPlaceholder != "Review diff carefully." {
		t.Fatalf("placeholder expansion = %q", withPlaceholder)
	}

	appended := Expand(Command{Body: "Review this code."}, "pkg/foo.go")
	if !strings.Contains(appended, "Review this code.") || !strings.Contains(appended, "Arguments:\npkg/foo.go") {
		t.Fatalf("appended expansion = %q", appended)
	}
}
