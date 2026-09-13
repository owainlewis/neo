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

// builtinNames lists the skills every Load result starts with.
var builtinNames = []string{"design", "plan", "build", "review"}

// discovered strips the built-ins so tests can assert on what Load found.
func discovered(sk []Skill) []Skill {
	var out []Skill
	for _, s := range sk {
		if !strings.HasPrefix(s.Path, "builtin:") {
			out = append(out, s)
		}
	}
	return out
}

func TestDefaults_ShipInProductOrderWithBodies(t *testing.T) {
	got := Defaults()
	if len(got) != len(builtinNames) {
		t.Fatalf("defaults = %d, want %d", len(got), len(builtinNames))
	}
	for i, name := range builtinNames {
		if got[i].Name != name || got[i].Description == "" || got[i].Body == "" {
			t.Fatalf("default %d = %+v, want %q with description and body", i, got[i], name)
		}
	}
}

func TestLoad_NoUserSkillsReturnsOnlyBuiltins(t *testing.T) {
	_, cwd, _ := repo(t)
	got, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(discovered(got)) != 0 || len(got) != len(builtinNames) {
		t.Fatalf("expected only built-ins, got %d skills", len(got))
	}
	for i, name := range builtinNames {
		if got[i].Name != name {
			t.Fatalf("built-in order lost: %d = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestLoad_ParsesFrontmatterAndBody(t *testing.T) {
	root, cwd, _ := repo(t)
	writeSkill(t, root, "audit", "---\nname: audit\ndescription: audit a diff\n---\nLook for bugs.")

	got, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	found := discovered(got)
	if len(found) != 1 {
		t.Fatalf("expected 1 discovered skill, got %d", len(found))
	}
	s := found[0]
	if s.Name != "audit" || s.Description != "audit a diff" || s.Body != "Look for bugs." {
		t.Fatalf("unexpected skill: %+v", s)
	}
	// Discovered skills follow the built-ins.
	if got[len(got)-1].Name != "audit" {
		t.Fatalf("discovered skill should come after built-ins, got %q last", got[len(got)-1].Name)
	}
}

func TestLoad_NameDefaultsToDirectory(t *testing.T) {
	root, cwd, _ := repo(t)
	writeSkill(t, root, "commit", "---\ndescription: write a commit\n---\nUse conventional commits.")
	got, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	found := discovered(got)
	if len(found) != 1 || found[0].Name != "commit" {
		t.Fatalf("expected name from directory, got %+v", found)
	}
}

func TestLoad_ProjectOverridesGlobal(t *testing.T) {
	root, cwd, home := repo(t)
	writeSkill(t, home, "audit", "---\ndescription: global\n---\nglobal body")
	writeSkill(t, root, "audit", "---\ndescription: project\n---\nproject body")

	got, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	found := discovered(got)
	if len(found) != 1 {
		t.Fatalf("expected 1 merged skill, got %d", len(found))
	}
	if found[0].Body != "project body" {
		t.Fatalf("project skill should win, got %q", found[0].Body)
	}
}

// A user skill named after a built-in replaces it in place: same slot in the
// order, user body and description.
func TestLoad_UserSkillReplacesBuiltin(t *testing.T) {
	root, cwd, _ := repo(t)
	writeSkill(t, root, "review", "---\ndescription: house review policy\n---\nApply the house policy.")

	got, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != len(builtinNames) {
		t.Fatalf("skills = %d, want the built-in count with review replaced", len(got))
	}
	review, ok := Find(got, "/review")
	if !ok || review.Body != "Apply the house policy." || review.Description != "house review policy" {
		t.Fatalf("review = %+v, want the project override", review)
	}
	if got[3].Name != "review" {
		t.Fatalf("override moved review out of its slot: %v", got)
	}
}

func TestLoad_SkipsBodylessSkill(t *testing.T) {
	root, cwd, _ := repo(t)
	writeSkill(t, root, "empty", "---\ndescription: nothing\n---\n")
	got, err := Load(cwd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if found := discovered(got); len(found) != 0 {
		t.Fatalf("expected bodyless skill skipped, got %v", found)
	}
}

func TestFindAndDisplayName(t *testing.T) {
	sk := Defaults()
	for _, q := range []string{"review", "/review", "$review", " Review "} {
		if s, ok := Find(sk, q); !ok || s.Name != "review" {
			t.Fatalf("Find(%q) = %+v, %v", q, s, ok)
		}
	}
	if _, ok := Find(sk, "/nope"); ok {
		t.Fatal("Find matched an unknown skill")
	}
	if got := DisplayName("code-review"); got != "Code review" {
		t.Fatalf("DisplayName = %q", got)
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

func TestExpand_PreservesWorkflowInstructions(t *testing.T) {
	const body = "Follow this workflow:\n1. Inspect the issue\n2. Make the change\n3. Review the diff"
	sk := []Skill{{Name: "implement", Body: body}}

	got, used := Expand("use $implement for issue 183", sk)

	if len(used) != 1 || used[0] != "implement" {
		t.Fatalf("expected used=[implement], got %v", used)
	}
	if !strings.Contains(got, body) {
		t.Fatalf("skill workflow was not preserved:\n%s", got)
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

func TestLoad_PreservesGoodSkillsAroundBadFiles(t *testing.T) {
	root, cwd, home := repo(t)
	writeSkill(t, home, "review", "global review")
	writeSkill(t, home, "global", "global skill")
	writeSkill(t, root, "review", "---\nname: [broken\n---\nbad")
	writeSkill(t, root, "oversized", strings.Repeat("x", 32*1024+1))
	writeSkill(t, root, "good", "project skill")
	writeSkill(t, root, "unreadable", "body")
	badPath := filepath.Join(root, ".neo", "skills", "unreadable", "SKILL.md")
	if err := os.Remove(badPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(badPath, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Load(cwd)
	if err == nil {
		t.Fatal("expected per-file diagnostics")
	}
	for _, name := range []string{"review", "oversized", "unreadable"} {
		if !strings.Contains(err.Error(), filepath.Join(name, "SKILL.md")) {
			t.Fatalf("missing warning for %s: %v", name, err)
		}
	}
	for name, body := range map[string]string{"review": "global review", "global": "global skill", "good": "project skill"} {
		s, ok := Find(got, name)
		if !ok || s.Body != body {
			t.Fatalf("%s = %+v, found %v", name, s, ok)
		}
	}
	if _, ok := Find(got, "oversized"); ok {
		t.Fatal("oversized skill loaded")
	}
}

func TestLoad_SymlinkedSkillDirectoryAndCRLF(t *testing.T) {
	_, cwd, home := repo(t)
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("---\r\nname: linked\r\ndescription: Linked skill\r\n---\r\nDo this.\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".neo", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	got, err := Load(cwd)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := Find(got, "linked")
	if !ok || s.Body != "Do this." || s.Description != "Linked skill" {
		t.Fatalf("linked skill = %+v, found %v", s, ok)
	}
}

func TestSplitFrontmatter_OnlyWholeLineClosesFence(t *testing.T) {
	s, err := parseSkill("fallback", []byte("---\r\nname: good\r\n---suffix\r\n"), "test")
	if err != nil || s.Name != "fallback" {
		t.Fatalf("partial fence treated as delimiter: %+v, %v", s, err)
	}
}
