package factory

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed defaults/*.md
var defaultSteps embed.FS

// Step is a resolved step definition. Agent steps carry a prompt (markdown
// body) plus frontmatter restrictions; script steps carry the executable path.
type Step struct {
	Name string
	Kind string // "agent" | "script"
	Path string // executable path for scripts; source path for agent steps ("" if embedded)

	// Agent-step fields, parsed from YAML frontmatter.
	Description string   // one-line summary, surfaced in the chat catalog
	Prompt      string   // system prompt body
	Tools       []string // allowed tool names; empty = read-only default (bash, read_file, grep, glob)
	Model       string   // pinned model; empty = inherit the session default
	MaxTurns    int      // agent loop turn cap; 0 = factory default
}

// Resolver locates steps by bare name. Search order: each path in Paths
// (project ./steps first, then ~/.neo/steps), then the embedded defaults.
// In a directory, <name>.md (agent step) wins over an executable <name>
// (script step).
type Resolver struct {
	Paths []string
}

// DefaultStepPaths returns the standard search path for a session rooted at dir.
func DefaultStepPaths(dir string) []string {
	paths := []string{filepath.Join(dir, "steps")}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".neo", "steps"))
	}
	return paths
}

// Resolve finds a step by bare name. Names are bare identifiers — no path
// separators or dots — so the model cannot traverse the filesystem. A step's
// name is its frontmatter `name`, falling back to its filename, so
// steps/step1.md with `name: first` is invoked as "first".
func (r Resolver) Resolve(name string) (Step, error) {
	if name == "" || strings.ContainsAny(name, "/\\.") {
		return Step{}, fmt.Errorf("step name %q: names are bare identifiers (no paths)", name)
	}
	for _, base := range r.Paths {
		if st, ok := findInDir(base, name); ok {
			return st, nil
		}
	}
	if b, err := defaultSteps.ReadFile("defaults/" + name + ".md"); err == nil {
		return parseAgentStep(name, "", b)
	}
	return Step{}, fmt.Errorf("no step named %q (available: %s)", name, strings.Join(r.List(), ", "))
}

// findInDir looks for the step in one directory: an exact-filename agent or
// script step first, then any .md whose frontmatter name matches. A
// frontmatter name is the step's only name — a renamed file no longer
// answers to its filename.
func findInDir(base, name string) (Step, bool) {
	md := filepath.Join(base, name+".md")
	if b, err := os.ReadFile(md); err == nil {
		if st, err := parseAgentStep(name, md, b); err == nil && st.Name == name {
			return st, true
		}
	}
	bin := filepath.Join(base, name)
	if info, err := os.Stat(bin); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
		return Step{Name: name, Kind: "script", Path: bin}, true
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return Step{}, false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(base, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		st, err := parseAgentStep(strings.TrimSuffix(e.Name(), ".md"), path, b)
		if err == nil && st.Name == name {
			return st, true
		}
	}
	return Step{}, false
}

// Catalog enumerates every available step across the search path and
// embedded defaults, deduplicated by name (earlier paths win, mirroring
// Resolve precedence), sorted by name. Used to advertise steps in the chat
// system prompt; scripts carry name only.
func (r Resolver) Catalog() []Step {
	byName := map[string]Step{}
	add := func(st Step) {
		if _, taken := byName[st.Name]; !taken {
			byName[st.Name] = st
		}
	}
	for _, base := range r.Paths {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			fname := e.Name()
			if strings.HasSuffix(fname, ".md") {
				b, err := os.ReadFile(filepath.Join(base, fname))
				if err != nil {
					continue
				}
				if st, err := parseAgentStep(strings.TrimSuffix(fname, ".md"), filepath.Join(base, fname), b); err == nil {
					add(st)
				}
				continue
			}
			if info, err := e.Info(); err == nil && info.Mode()&0o111 != 0 {
				add(Step{Name: fname, Kind: "script", Path: filepath.Join(base, fname)})
			}
		}
	}
	if entries, err := defaultSteps.ReadDir("defaults"); err == nil {
		for _, e := range entries {
			b, err := defaultSteps.ReadFile("defaults/" + e.Name())
			if err != nil {
				continue
			}
			if st, err := parseAgentStep(strings.TrimSuffix(e.Name(), ".md"), "", b); err == nil {
				add(st)
			}
		}
	}
	out := make([]Step, 0, len(byName))
	for _, st := range byName {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// List enumerates available step names, deduplicated, sorted.
func (r Resolver) List() []string {
	cat := r.Catalog()
	out := make([]string, len(cat))
	for i, st := range cat {
		out[i] = st.Name
	}
	return out
}

func parseAgentStep(name, path string, content []byte) (Step, error) {
	fm, body := splitFrontmatter(content)
	st := Step{Name: name, Kind: "agent", Path: path, Prompt: strings.TrimSpace(string(body))}
	if len(fm) > 0 {
		var meta struct {
			Name        string   `yaml:"name"`
			Description string   `yaml:"description"`
			Tools       []string `yaml:"tools"`
			Model       string   `yaml:"model"`
			MaxTurns    int      `yaml:"max_turns"`
		}
		if err := yaml.Unmarshal(fm, &meta); err != nil {
			return Step{}, fmt.Errorf("step %s: frontmatter: %w", name, err)
		}
		if n := strings.ToLower(strings.TrimSpace(meta.Name)); n != "" {
			st.Name = n
		}
		st.Description = strings.TrimSpace(meta.Description)
		st.Tools = meta.Tools
		st.Model = strings.TrimSpace(meta.Model)
		st.MaxTurns = meta.MaxTurns
	}
	if st.Prompt == "" {
		return Step{}, fmt.Errorf("step %s: empty prompt body", name)
	}
	return st, nil
}

// splitFrontmatter separates optional leading `---`-fenced YAML frontmatter
// from the body. With no frontmatter it returns (nil, content).
func splitFrontmatter(content []byte) (fm, body []byte) {
	s := string(content)
	if !strings.HasPrefix(s, "---\n") {
		return nil, content
	}
	rest := s[len("---\n"):]
	for _, sep := range []string{"\n---\n", "\n---"} {
		if i := strings.Index(rest, sep); i >= 0 {
			return []byte(rest[:i]), []byte(strings.TrimLeft(rest[i+len(sep):], "\n"))
		}
	}
	return nil, content // unterminated — treat the whole file as body
}
