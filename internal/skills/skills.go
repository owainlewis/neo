// Package skills discovers user-defined skills (SKILL.md files) and surfaces
// them to the chat agent in three ways:
//
//   - Advertise: a lightweight catalog (name + description) is composed into the
//     system prompt via Augment, so the model knows which skills exist.
//   - Expand: when the user's message mentions a skill by `$name`, Expand pulls
//     that skill's full body into the turn.
//   - ExpandInvocation: when the user invokes `/name args`, ExpandInvocation
//     turns the skill body plus args into the agent turn.
//
// Like projectctx, this is a layered capability gated by a feature flag
// (features.skills) and wired at the chat surface — the core agent loop is
// untouched.
package skills

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/owainlewis/neo/internal/promptfile"
	"github.com/owainlewis/neo/internal/workspace"
)

const fileName = "SKILL.md"

// defaultsFS holds the skills Neo ships with: design, plan, build, and
// review. Each is an ordinary SKILL.md body stored flat as <name>.md (a
// build/ directory would trip common global gitignores), so a user or project
// skill with the same name replaces one outright.
//
//go:embed defaults/*.md
var defaultsFS embed.FS

// Defaults returns fresh copies of the built-in skills in product order.
func Defaults() []Skill {
	var out []Skill
	for _, name := range []string{"design", "plan", "build", "review"} {
		path := "defaults/" + name + ".md"
		b, err := fs.ReadFile(defaultsFS, path)
		if err != nil {
			panic("skills: missing embedded default " + path)
		}
		s, err := parseSkill(name, b, "builtin:"+name)
		if err != nil {
			panic("skills: " + err.Error())
		}
		out = append(out, s)
	}
	return out
}

// Skill is one discovered skill: how it's invoked, what it's for, and the body
// that gets expanded into a turn when invoked.
type Skill struct {
	Name        string // invocation name (lowercased); referenced as $name or /name
	Description string // one-line summary, shown in the advertised catalog
	Body        string // full instructions, expanded when invoked
	Path        string // source SKILL.md path
}

// Load returns the built-in skills plus those discovered for a session rooted
// at cwd. Discovery looks in:
//
//   - ~/.neo/skills/<name>/SKILL.md            user-global
//   - <repo-or-cwd>/.neo/skills/<name>/SKILL.md   project (overrides global)
//
// A skill's invocation name is its frontmatter `name`, falling back to the
// directory name. Later sources override earlier ones of the same name, so a
// project skill beats a global one and both beat a built-in. Missing
// directories are skipped. Read/parse errors are returned alongside valid skills.
//
// Built-ins keep their product order at the front of the result; discovered
// skills follow sorted by name.
func Load(cwd string) ([]Skill, error) {
	var loadErrs []error
	byName := map[string]Skill{}
	defaults := Defaults()
	for _, s := range defaults {
		byName[s.Name] = s
	}

	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".neo", "skills"))
	}
	dirs = append(dirs, filepath.Join(workspace.Root(cwd), ".neo", "skills"))

	// Later directories override earlier ones (project beats global).
	for _, dir := range dirs {
		found, err := loadDir(dir)
		if err != nil {
			loadErrs = append(loadErrs, err)
		}
		for _, s := range found {
			byName[s.Name] = s
		}
	}

	return order(defaults, byName), errors.Join(loadErrs...)
}

// order places built-ins first in product order, then the rest by name.
func order(defaults []Skill, byName map[string]Skill) []Skill {
	out := make([]Skill, 0, len(byName))
	builtin := map[string]bool{}
	for _, d := range defaults {
		builtin[d.Name] = true
		out = append(out, byName[d.Name])
	}
	var rest []Skill
	for name, s := range byName {
		if !builtin[name] {
			rest = append(rest, s)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Name < rest[j].Name })
	return append(out, rest...)
}

// Find returns one skill by its case-insensitive invocation name, with or
// without a leading slash or dollar sign.
func Find(sk []Skill, name string) (Skill, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(strings.TrimPrefix(name, "/"), "$")
	for _, s := range sk {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// DisplayName turns an invocation name into a compact UI label: "code-review"
// becomes "Code review".
func DisplayName(name string) string {
	name = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(name), "-", " "), "_", " ")
	if name == "" {
		return "Skill"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func loadDir(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []Skill
	var loadErrs []error
	for _, e := range entries {
		entryPath := filepath.Join(dir, e.Name())
		info, err := os.Stat(entryPath)
		if err != nil {
			loadErrs = append(loadErrs, fmt.Errorf("stat %s: %w", entryPath, err))
			continue
		}
		if !info.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), fileName)
		b, err := promptfile.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			loadErrs = append(loadErrs, fmt.Errorf("read %s: %w", path, err))
			continue
		}
		s, err := parseSkill(e.Name(), b, path)
		if err != nil {
			loadErrs = append(loadErrs, err)
			continue
		}
		if s.Body == "" {
			continue // nothing to expand; skip
		}
		out = append(out, s)
	}
	return out, errors.Join(loadErrs...)
}

func parseSkill(dirName string, content []byte, path string) (Skill, error) {
	fm, body := splitFrontmatter(content)
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if len(fm) > 0 {
		if err := yaml.Unmarshal(fm, &meta); err != nil {
			return Skill{}, fmt.Errorf("%s: frontmatter: %w", path, err)
		}
	}
	name := meta.Name
	if name == "" {
		name = dirName
	}
	return Skill{
		Name:        strings.ToLower(strings.TrimSpace(name)),
		Description: strings.TrimSpace(meta.Description),
		Body:        strings.TrimSpace(string(body)),
		Path:        path,
	}, nil
}

// splitFrontmatter separates optional leading `---`-fenced YAML frontmatter from
// the body. With no frontmatter it returns (nil, content).
func splitFrontmatter(content []byte) (fm, body []byte) {
	s := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, content
	}
	rest := s[len("---\n"):]
	offset := 0
	for _, line := range strings.Split(rest, "\n") {
		if line == "---" {
			return []byte(rest[:offset]), []byte(strings.TrimLeft(rest[offset+len(line):], "\n"))
		}
		offset += len(line) + 1
	}
	return nil, content // unterminated — treat the whole file as body
}

// Augment appends a catalog of available skills (name + description only) to a
// base system prompt, so the model is aware of them. Returns base unchanged when
// there are no skills.
func Augment(base string, sk []Skill) string {
	if len(sk) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n# Available skills\n\n")
	b.WriteString("Skills are focused instructions for one turn. The user invokes one by ")
	b.WriteString("mentioning `$name` in a message or by running `/name args`; its instructions are then expanded into ")
	b.WriteString("that turn. You may also suggest a relevant skill by name.\n")
	for _, s := range sk {
		b.WriteString("\n- `$")
		b.WriteString(s.Name)
		b.WriteString("`")
		if s.Description != "" {
			b.WriteString(" — ")
			b.WriteString(s.Description)
		}
	}
	return b.String()
}

// refRe matches a `$name` skill reference.
var refRe = regexp.MustCompile(`\$([a-zA-Z0-9_-]+)`)

// Expand scans input for `$name` references to known skills and returns the
// input prefixed with the body of each referenced skill (in order of first
// mention, each at most once), plus the names applied. When no known skill is
// referenced it returns input unchanged and a nil slice — an unrecognized
// `$foo` is left alone.
func Expand(input string, sk []Skill) (string, []string) {
	if len(sk) == 0 {
		return input, nil
	}
	byName := make(map[string]Skill, len(sk))
	for _, s := range sk {
		byName[s.Name] = s
	}

	var used []string
	seen := map[string]bool{}
	for _, m := range refRe.FindAllStringSubmatch(input, -1) {
		name := strings.ToLower(m[1])
		if _, ok := byName[name]; ok && !seen[name] {
			seen[name] = true
			used = append(used, name)
		}
	}
	if len(used) == 0 {
		return input, nil
	}

	var b strings.Builder
	for _, name := range used {
		s := byName[name]
		fmt.Fprintf(&b, "[skill: %s]\n%s\n\n", s.Name, s.Body)
	}
	b.WriteString(input)
	return b.String(), used
}

// ExpandInvocation renders one slash-invoked skill body with optional trailing
// arguments. The skill body is always included, and args are appended in a
// labelled section so the model can distinguish workflow instructions from the
// user's task-specific input.
func ExpandInvocation(s Skill, args string) string {
	body := strings.TrimSpace(s.Body)
	args = strings.TrimSpace(args)
	if args == "" {
		return fmt.Sprintf("[skill: %s]\n%s", s.Name, body)
	}
	return fmt.Sprintf("[skill: %s]\n%s\n\nArguments:\n%s", s.Name, body, args)
}
