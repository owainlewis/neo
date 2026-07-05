// Package promptcmd discovers markdown-backed slash commands.
//
// Prompt commands are lightweight prompt templates. They are not scripts and
// are expanded only when the user invokes /name from the chat UI.
package promptcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/owainlewis/neo/internal/workspace"
)

// Command is one discovered prompt-file slash command.
type Command struct {
	Name        string
	Description string
	Body        string
	Path        string
}

// Load discovers commands from:
//
//   - ~/.neo/commands/*.md
//   - <repo-or-cwd>/.neo/commands/*.md
//
// Project commands override user-global commands of the same name. Missing
// directories and empty command files are skipped. Malformed files are returned
// as warnings so the TUI can start and surface a concise warning.
func Load(cwd string) ([]Command, []error) {
	byName := map[string]Command{}
	var warnings []error

	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".neo", "commands"))
	}
	dirs = append(dirs, filepath.Join(workspace.Root(cwd), ".neo", "commands"))

	for _, dir := range dirs {
		found, errs := loadDir(dir)
		warnings = append(warnings, errs...)
		for _, c := range found {
			byName[c.Name] = c
		}
	}

	out := make([]Command, 0, len(byName))
	for _, c := range byName {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, warnings
}

func loadDir(dir string) ([]Command, []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("read %s: %w", dir, err)}
	}
	var out []Command
	var warnings []error
	for _, e := range entries {
		if e.IsDir() || strings.ToLower(filepath.Ext(e.Name())) != ".md" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("read %s: %w", path, err))
			continue
		}
		cmd, err := parseCommand(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())), b, path)
		if err != nil {
			warnings = append(warnings, err)
			continue
		}
		if cmd.Body == "" {
			continue
		}
		out = append(out, cmd)
	}
	return out, warnings
}

func parseCommand(fileName string, content []byte, path string) (Command, error) {
	fm, body := splitFrontmatter(content)
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if len(fm) > 0 {
		if err := yaml.Unmarshal(fm, &meta); err != nil {
			return Command{}, fmt.Errorf("%s: frontmatter: %w", path, err)
		}
	}
	name := commandName(meta.Name)
	if name == "" {
		name = commandName(fileName)
	}
	if name == "" {
		return Command{}, fmt.Errorf("%s: command name is empty", path)
	}
	bodyText := strings.TrimSpace(string(body))
	return Command{
		Name:        name,
		Description: commandDescription(meta.Description, bodyText),
		Body:        bodyText,
		Path:        path,
	}, nil
}

func commandName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.TrimPrefix(name, "/")
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func commandDescription(desc, body string) string {
	desc = strings.TrimSpace(desc)
	if desc != "" {
		return desc
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line != "" {
			return line
		}
	}
	return "prompt command"
}

// Expand renders a command body with optional trailing arguments.
func Expand(cmd Command, args string) string {
	body := strings.TrimSpace(cmd.Body)
	args = strings.TrimSpace(args)
	if args == "" {
		return body
	}
	replaced := strings.ReplaceAll(body, "$ARGUMENTS", args)
	replaced = strings.ReplaceAll(replaced, "{{args}}", args)
	if replaced != body {
		return replaced
	}
	return body + "\n\nArguments:\n" + args
}

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
	return []byte(rest), nil
}
