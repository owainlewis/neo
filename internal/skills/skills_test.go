package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repo lays out a temp tree that looks like a git repo, with an isolated HOME.
func repo(t *testing.T) (root, cwd, home string) {
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

func writeSkill(t *testing.T, base, name, body string) {
	t.Helper()
	dir := filepath.Join(base, ".neo", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_NoSkillsReturnsEmpty(t *testing.T) {
	_, cwd, _ := repo(t)
	got, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no skills, got %v", got)
	}
}

func TestLoad_ParsesFrontmatterAndBody(t *testing.T) {
	root, cwd, _ := repo(t)
	writeSkill(t, root, "review", "---\nname: review\ndescription: audit a diff\n---\nLook for bugs.")

	got, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(got))
	}
	s := got[0]
	if s.Name != "review" || s.Description != "audit a diff" || s.Body != "Look for bugs." {
		t.Fatalf("unexpected skill: %+v", s)
	}
}

func TestLoad_NameDefaultsToDirectory(t *testing.T) {
	root, cwd, _ := repo(t)
	writeSkill(t, root, "commit", "---\ndescription: write a commit\n---\nUse conventional commits.")
	got, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || got[0].Name != "commit" {
		t.Fatalf("expected name from directory, got %+v", got)
	}
}

func TestLoad_ProjectOverridesGlobal(t *testing.T) {
	root, cwd, home := repo(t)
	writeSkill(t, home, "review", "---\ndescription: global\n---\nglobal body")
	writeSkill(t, root, "review", "---\ndescription: project\n---\nproject body")

	got, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 merged skill, got %d", len(got))
	}
	if got[0].Body != "project body" {
		t.Fatalf("project skill should win, got %q", got[0].Body)
	}
}

func TestLoad_SkipsBodylessSkill(t *testing.T) {
	root, cwd, _ := repo(t)
	writeSkill(t, root, "empty", "---\ndescription: nothing\n---\n")
	got, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected bodyless skill skipped, got %v", got)
	}
}

func TestAugment_NoSkillsUnchanged(t *testing.T) {
	if got := Augment("base", nil); got != "base" {
		t.Fatalf("expected base unchanged, got %q", got)
	}
}

func TestAugment_ListsNameAndDescription(t *testing.T) {
	out := Augment("base", []Skill{
		{Name: "review", Description: "audit a diff", Body: "x"},
		{Name: "commit", Description: "write a commit", Body: "y"},
	})
	for _, want := range []string{"# Available skills", "$review", "audit a diff", "$commit", "write a commit", "`/name args`"} {
		if !strings.Contains(out, want) {
			t.Errorf("catalog missing %q:\n%s", want, out)
		}
	}
	// Bodies must NOT be in the catalog — only name + description.
	if strings.Contains(out, "\nx") {
		t.Errorf("catalog should not include skill bodies:\n%s", out)
	}
}

func TestExpand_NoReferenceUnchanged(t *testing.T) {
	sk := []Skill{{Name: "review", Body: "B"}}
	got, used := Expand("just chatting", sk)
	if got != "just chatting" || used != nil {
		t.Fatalf("expected unchanged, got %q used=%v", got, used)
	}
}

func TestExpand_UnknownReferenceLeftAlone(t *testing.T) {
	sk := []Skill{{Name: "review", Body: "B"}}
	got, used := Expand("echo $HOME and $nope", sk)
	if got != "echo $HOME and $nope" || used != nil {
		t.Fatalf("unknown refs must be left alone, got %q used=%v", got, used)
	}
}

func TestExpand_InjectsBodyAndReportsUse(t *testing.T) {
	sk := []Skill{{Name: "review", Body: "Look for bugs."}}
	got, used := Expand("use the $review skill on my diff", sk)
	if len(used) != 1 || used[0] != "review" {
		t.Fatalf("expected used=[review], got %v", used)
	}
	if !strings.Contains(got, "Look for bugs.") {
		t.Errorf("expected body injected, got:\n%s", got)
	}
	if !strings.HasSuffix(got, "use the $review skill on my diff") {
		t.Errorf("original input should be preserved at the end, got:\n%s", got)
	}
}

func TestExpand_EachSkillOnceInOrder(t *testing.T) {
	sk := []Skill{{Name: "a", Body: "AA"}, {Name: "b", Body: "BB"}}
	got, used := Expand("$b then $a then $b again", sk)
	if strings.Join(used, ",") != "b,a" {
		t.Fatalf("expected first-mention order without dupes, got %v", used)
	}
	if strings.Count(got, "BB") != 1 {
		t.Errorf("skill b should be expanded once, got:\n%s", got)
	}
}

func TestExpandInvocation_IncludesBodyAndArguments(t *testing.T) {
	got := ExpandInvocation(Skill{Name: "review", Body: "Look for bugs."}, "internal/tui")
	want := "[skill: review]\nLook for bugs.\n\nArguments:\ninternal/tui"
	if got != want {
		t.Fatalf("expanded invocation = %q, want %q", got, want)
	}
}
