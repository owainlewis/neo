package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workspaceWithAgents builds a repo whose .neo/agents holds the given files,
// with HOME pointed at a separate tree so global profiles can be added too.
func workspaceWithAgents(t *testing.T, files map[string]string) (cwd, home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	cwd = t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAgents(t, filepath.Join(cwd, ".neo", "agents"), files)
	return cwd, home
}

func writeAgents(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if len(files) == 0 {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoad_ReturnsTheBody(t *testing.T) {
	cwd, _ := workspaceWithAgents(t, map[string]string{
		"assistant.md": "\nYou are a calm personal assistant.\n\n",
	})

	got, err := Load(cwd, "assistant")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Name != "assistant" {
		t.Fatalf("name = %q", got.Name)
	}
	if got.Body != "You are a calm personal assistant." {
		t.Fatalf("body = %q, want it trimmed", got.Body)
	}
}

func TestLoad_ProjectShadowsGlobal(t *testing.T) {
	cwd, home := workspaceWithAgents(t, map[string]string{"reviewer.md": "project version"})
	writeAgents(t, filepath.Join(home, ".neo", "agents"), map[string]string{
		"reviewer.md": "global version",
		"solo.md":     "only global",
	})

	got, err := Load(cwd, "reviewer")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Body != "project version" {
		t.Fatalf("body = %q, want the project file to win", got.Body)
	}
	if solo, err := Load(cwd, "solo"); err != nil || solo.Body != "only global" {
		t.Fatalf("global-only profile should still resolve: %+v %v", solo, err)
	}
}

func TestLoad_UnknownNameListsWhatExists(t *testing.T) {
	cwd, _ := workspaceWithAgents(t, map[string]string{"reviewer.md": "x", "architect.md": "y"})

	_, err := Load(cwd, "assistant")
	if err == nil {
		t.Fatal("an unknown agent must be an error, not a silent fallback")
	}
	for _, want := range []string{`"assistant" not found`, "architect", "reviewer"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

func TestLoad_NoAgentsDefinedSaysWhereToPutThem(t *testing.T) {
	cwd, _ := workspaceWithAgents(t, nil)

	_, err := Load(cwd, "assistant")
	if err == nil || !strings.Contains(err.Error(), "no agents defined") {
		t.Fatalf("error = %v, want guidance on where to create one", err)
	}
}

func TestLoad_RejectsPathTraversal(t *testing.T) {
	cwd, _ := workspaceWithAgents(t, map[string]string{"ok.md": "x"})

	for _, name := range []string{"../secrets", "sub/nested", ""} {
		if _, err := Load(cwd, name); err == nil {
			t.Fatalf("Load(%q) should fail", name)
		}
	}
}

func TestList_SkipsNonMarkdownAndEmptyFiles(t *testing.T) {
	cwd, _ := workspaceWithAgents(t, map[string]string{
		"good.md":   "instructions",
		"blank.md":  "   \n\n",
		"notes.txt": "not a profile",
		"UPPER.MD":  "case insensitive extension",
	})

	found, err := List(cwd)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var names []string
	for _, p := range found {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "good,upper" {
		t.Fatalf("names = %v, want good and upper only", names)
	}
}
