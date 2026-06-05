package tools

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/owainlewis/neo/internal/llm"
)

const defaultSearchMax = 200

type Grep struct {
	Root string
}

func (Grep) Name() string { return "grep" }

func (Grep) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "grep",
		Description: "Search text files under the workspace with a regular expression. Returns file:line matches with optional context.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":       map[string]any{"type": "string", "description": "Regular expression to search for"},
				"path":          map[string]any{"type": "string", "description": "File or directory under the workspace root (optional)"},
				"context_lines": map[string]any{"type": "integer", "description": "Number of context lines before and after each match (optional)"},
				"max_matches":   map[string]any{"type": "integer", "description": "Maximum matches to return (optional, default 200)"},
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
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}
	target, err := scopedPath(g.Root, optString(input, "path"))
	if err != nil {
		return "", err
	}
	contextLines := optInt(input, "context_lines")
	if contextLines < 0 {
		contextLines = 0
	}
	maxMatches := optInt(input, "max_matches")
	if maxMatches <= 0 {
		maxMatches = defaultSearchMax
	}

	files, err := filesUnder(target)
	if err != nil {
		return "", err
	}
	var out []string
	matchCount := 0
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return strings.Join(out, "\n"), err
		}
		lines, err := readTextLines(file)
		if err != nil {
			continue
		}
		for i, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			if matchCount >= maxMatches {
				out = append(out, fmt.Sprintf("truncated after %d matches", maxMatches))
				return strings.Join(out, "\n"), nil
			}
			matchCount++
			start := max(0, i-contextLines)
			end := min(len(lines), i+contextLines+1)
			for n := start; n < end; n++ {
				prefix := ":"
				if n == i {
					prefix = ">"
				}
				out = append(out, fmt.Sprintf("%s%s%d:%s", displayPath(g.Root, file), prefix, n+1, lines[n]))
			}
		}
	}
	if len(out) == 0 {
		return "no matches", nil
	}
	return strings.Join(out, "\n"), nil
}

type Glob struct {
	Root string
}

func (Glob) Name() string { return "glob" }

func (Glob) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "glob",
		Description: "Find files under the workspace root using a glob pattern. Supports ** for recursive matches.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":     map[string]any{"type": "string", "description": "Glob pattern such as **/*.go"},
				"path":        map[string]any{"type": "string", "description": "Directory under the workspace root to search from (optional)"},
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
	base, err := scopedPath(g.Root, optString(input, "path"))
	if err != nil {
		return "", err
	}
	maxMatches := optInt(input, "max_matches")
	if maxMatches <= 0 {
		maxMatches = defaultSearchMax
	}
	var matches []string
	truncated := false
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) && path != base {
				return filepath.SkipDir
			}
			return nil
		}
		relBase, err := filepath.Rel(base, path)
		if err != nil {
			return nil
		}
		relRoot := displayPath(g.Root, path)
		ok, err := doublestarMatch(pattern, relBase)
		if err != nil {
			return err
		}
		if !ok && base != g.Root {
			ok, err = doublestarMatch(pattern, relRoot)
			if err != nil {
				return err
			}
		}
		if ok {
			matches = append(matches, relRoot)
			if len(matches) >= maxMatches {
				truncated = true
				return errStopWalk
			}
		}
		return nil
	})
	if err != nil && err != errStopWalk {
		return "", err
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return "no matches", nil
	}
	if truncated {
		matches = append(matches, fmt.Sprintf("truncated after %d matches", maxMatches))
	}
	return strings.Join(matches, "\n"), nil
}

var errStopWalk = fmt.Errorf("stop walk")

func scopedPath(root, path string) (string, error) {
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if path == "" {
		path = absRoot
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(absRoot, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absRoot = filepath.Clean(absRoot)
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(absRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside workspace root %s", path, absRoot)
	}
	return abs, nil
}

func filesUnder(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	var files []string
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) && p != path {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, p)
		return nil
	})
	sort.Strings(files)
	return files, err
}

func readTextLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024), MaxReadBytes)
	var lines []string
	for sc.Scan() {
		line := sc.Text()
		if strings.ContainsRune(line, 0) {
			return nil, fmt.Errorf("binary file")
		}
		lines = append(lines, line)
	}
	return lines, sc.Err()
}

func displayPath(root, path string) string {
	if root == "" {
		return path
	}
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "dist", "vendor":
		return true
	default:
		return false
	}
}

func doublestarMatch(pattern, name string) (bool, error) {
	pattern = filepath.ToSlash(pattern)
	name = filepath.ToSlash(name)
	return matchParts(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchParts(pattern, name []string) (bool, error) {
	if len(pattern) == 0 {
		return len(name) == 0, nil
	}
	if pattern[0] == "**" {
		if ok, err := matchParts(pattern[1:], name); ok || err != nil {
			return ok, err
		}
		for i := range name {
			if ok, err := matchParts(pattern[1:], name[i+1:]); ok || err != nil {
				return ok, err
			}
		}
		return false, nil
	}
	if len(name) == 0 {
		return false, nil
	}
	ok, err := filepath.Match(pattern[0], name[0])
	if err != nil || !ok {
		return ok, err
	}
	return matchParts(pattern[1:], name[1:])
}
