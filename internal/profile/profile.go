// Package profile loads agent profiles: markdown files that replace Neo's
// built-in system prompt so one binary can be a coding agent, a personal
// assistant, or anything else the user writes down.
//
// A profile is deliberately just a file. There is no frontmatter, no schema,
// and no config block, because the whole appeal is that writing one requires
// learning nothing.
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/owainlewis/neo/internal/workspace"
)

const dirName = "agents"

// Profile is one discovered agent prompt.
type Profile struct {
	Name string // invocation name, the file name without .md
	Body string // instructions, replacing the built-in base prompt
	Path string // source file, shown when listing
}

// dirs returns the profile directories in increasing priority: user-global
// first, then the project, so a repo can shadow a personal profile by name.
func dirs(cwd string) []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, filepath.Join(home, ".neo", dirName))
	}
	if cwd != "" {
		out = append(out, filepath.Join(workspace.Root(cwd), ".neo", dirName))
	}
	return out
}

// Load returns the named profile. An unknown name is an error listing what is
// available: falling back to the built-in prompt would turn a typo into a
// silently wrong agent.
func Load(cwd, name string) (Profile, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return Profile{}, fmt.Errorf("agent: name is required")
	}
	if name != filepath.Base(name) || strings.Contains(name, string(filepath.Separator)) {
		return Profile{}, fmt.Errorf("agent %q: name must not contain a path", name)
	}

	found, err := List(cwd)
	if err != nil {
		return Profile{}, err
	}
	for _, p := range found {
		if p.Name == name {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("agent %q not found%s", name, availableSuffix(found, cwd))
}

func availableSuffix(found []Profile, cwd string) string {
	if len(found) == 0 {
		return "; no agents defined. Create one at " + strings.Join(dirs(cwd), " or ") + "/<name>.md"
	}
	names := make([]string, 0, len(found))
	for _, p := range found {
		names = append(names, p.Name)
	}
	return "; available: " + strings.Join(names, ", ")
}

// List returns every discovered profile, sorted by name, with project files
// shadowing user-global ones. A missing directory is not an error.
func List(cwd string) ([]Profile, error) {
	byName := map[string]Profile{}
	for _, dir := range dirs(cwd) {
		found, err := loadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, p := range found {
			byName[p.Name] = p
		}
	}
	out := make([]Profile, 0, len(byName))
	for _, p := range byName {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func loadDir(dir string) ([]Profile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []Profile
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		body := strings.TrimSpace(string(b))
		if body == "" {
			continue // nothing to say; not a usable prompt
		}
		out = append(out, Profile{
			Name: strings.ToLower(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))),
			Body: body,
			Path: path,
		})
	}
	return out, nil
}
