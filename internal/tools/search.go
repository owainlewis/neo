package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/owainlewis/neo/internal/llm"
)

// Search tools shell out to ripgrep rather than walking the tree themselves.
// rg respects .gitignore, skips binaries, and is an order of magnitude faster
// than anything worth hand-rolling here.
//
// They remain distinct tools rather than folding into bash for two reasons:
// they are classified parallel-safe, which bash cannot be without interpreting
// shell commands, and they give inspect-mode subagents a read-only search
// capability that does not require handing them a shell.
const (
	// SearchBinary is exported so `neo doctor` can report whether search works.
	SearchBinary     = "rg"
	defaultSearchMax = 200
)

// errNoRipgrep tells the model what to do instead. Reimplementing search in Go
// as a fallback would mean two code paths with different ignore rules and
// different output, which is worse than one clear message.
var errNoRipgrep = fmt.Errorf(
	"%s is not installed, so grep and glob are unavailable; use bash with grep or find instead, or install ripgrep", SearchBinary)

// baseArgs are shared by both tools. --no-require-git makes .gitignore apply
// even when the workspace is not a git checkout, so discovery does not silently
// change behaviour depending on whether the directory happens to be a repo.
func baseArgs() []string {
	return []string{"--color", "never", "--no-require-git"}
}

type Grep struct {
	Root string
}

func (Grep) Name() string { return "grep" }

func (Grep) ParallelSafe(map[string]any) bool { return true }

func (Grep) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "grep",
		Description: "Search the workspace with a regular expression, honouring .gitignore. Returns matching lines as path:line:text.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":       map[string]any{"type": "string", "description": "Regular expression to search for"},
				"path":          map[string]any{"type": "string", "description": "File or directory to search (optional, defaults to the workspace root)"},
				"context_lines": map[string]any{"type": "integer", "description": "Lines of context before and after each match (optional)"},
				"max_matches":   map[string]any{"type": "integer", "description": "Maximum matching lines to return (optional, default 200)"},
			},
			"required": []string{"pattern"},
		},
	}
}

func (g Grep) Run(ctx context.Context, input map[string]any) (string, error) {
	pattern, err := mustString(input, "pattern")
	if err != nil {
		return "", err
	}
	args := append(baseArgs(), "--line-number", "--no-heading")
	if n := optInt(input, "context_lines"); n > 0 {
		args = append(args, "--context", fmt.Sprint(n))
	}
	args = append(args, "--regexp", pattern)
	if path := optString(input, "path"); path != "" {
		args = append(args, path)
	}
	return runSearch(ctx, g.Root, args, optInt(input, "max_matches"), "matching lines")
}

type Glob struct {
	Root string
}

func (Glob) Name() string { return "glob" }

func (Glob) ParallelSafe(map[string]any) bool { return true }

func (Glob) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "glob",
		Description: "List workspace files matching a glob pattern, honouring .gitignore. Supports ** for recursive matches. Returns one path per line.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":     map[string]any{"type": "string", "description": "Glob pattern such as **/*.go"},
				"path":        map[string]any{"type": "string", "description": "Directory to search from (optional, defaults to the workspace root)"},
				"max_matches": map[string]any{"type": "integer", "description": "Maximum paths to return (optional, default 200)"},
			},
			"required": []string{"pattern"},
		},
	}
}

func (g Glob) Run(ctx context.Context, input map[string]any) (string, error) {
	pattern, err := mustString(input, "pattern")
	if err != nil {
		return "", err
	}
	args := append(baseArgs(), "--files", "--glob", pattern)
	if path := optString(input, "path"); path != "" {
		args = append(args, path)
	}
	return runSearch(ctx, g.Root, args, optInt(input, "max_matches"), "paths")
}

// runSearch executes ripgrep and returns at most maxMatches lines. rg exits 1
// when nothing matched, which is a result rather than a failure.
func runSearch(ctx context.Context, root string, args []string, maxMatches int, unit string) (string, error) {
	if maxMatches <= 0 {
		maxMatches = defaultSearchMax
	}
	cmd := exec.CommandContext(ctx, SearchBinary, args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", errNoRipgrep
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() == 1 {
				return "no matches", nil
			}
			return "", fmt.Errorf("%s: %s", SearchBinary, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	return truncateLines(strings.TrimRight(string(out), "\n"), maxMatches, unit), nil
}

func truncateLines(out string, limit int, unit string) string {
	if out == "" {
		return "no matches"
	}
	lines := strings.Split(out, "\n")
	if len(lines) <= limit {
		return out
	}
	return strings.Join(lines[:limit], "\n") +
		fmt.Sprintf("\n\n[showing the first %d of %d %s; narrow the pattern or raise max_matches]", limit, len(lines), unit)
}
