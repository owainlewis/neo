package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/workspace"
)

const defaultSearchMax = 200

type grepResult struct {
	Matches   []grepMatch `json:"matches"`
	Truncated bool        `json:"truncated"`
	Count     int         `json:"count"`
}

type grepMatch struct {
	Path          string            `json:"path"`
	Line          int               `json:"line"`
	Text          string            `json:"text"`
	ContextBefore []grepContextLine `json:"context_before,omitempty"`
	ContextAfter  []grepContextLine `json:"context_after,omitempty"`
}

type grepContextLine struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

type globResult struct {
	Matches   []string `json:"matches"`
	Truncated bool     `json:"truncated"`
	Count     int      `json:"count"`
}

type Grep struct {
	Root string
}

func (Grep) Name() string { return "grep" }

func (Grep) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "grep",
		Description: "Search text files under the workspace with a regular expression. Returns JSON: {matches:[{path,line,text,context_before?,context_after?}],truncated,count}.",
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
	displayRoot, err := scopedPath(g.Root, "")
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
	result := grepResult{Matches: []grepMatch{}}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			result.Count = len(result.Matches)
			out, jsonErr := encodeSearchResult(result)
			if jsonErr != nil {
				return "", jsonErr
			}
			return out, err
		}
		lines, err := readTextLines(file)
		if err != nil {
			continue
		}
		for i, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			if len(result.Matches) >= maxMatches {
				result.Truncated = true
				result.Count = len(result.Matches)
				return encodeSearchResult(result)
			}
			match := grepMatch{
				Path: displayPath(displayRoot, file),
				Line: i + 1,
				Text: line,
			}
			beforeStart := max(0, i-contextLines)
			for n := beforeStart; n < i; n++ {
				match.ContextBefore = append(match.ContextBefore, grepContextLine{Line: n + 1, Text: lines[n]})
			}
			afterEnd := min(len(lines), i+contextLines+1)
			for n := i + 1; n < afterEnd; n++ {
				match.ContextAfter = append(match.ContextAfter, grepContextLine{Line: n + 1, Text: lines[n]})
			}
			result.Matches = append(result.Matches, match)
		}
	}
	result.Count = len(result.Matches)
	return encodeSearchResult(result)
}

type Glob struct {
	Root string
}

func (Glob) Name() string { return "glob" }

func (Glob) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "glob",
		Description: "Find files under the workspace root using a glob pattern. Supports ** for recursive matches. Returns JSON: {matches:[path],truncated,count}.",
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
	displayRoot, err := scopedPath(g.Root, "")
	if err != nil {
		return "", err
	}
	maxMatches := optInt(input, "max_matches")
	if maxMatches <= 0 {
		maxMatches = defaultSearchMax
	}
	result := globResult{Matches: []string{}}
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
		relRoot := displayPath(displayRoot, path)
		ok, err := doublestarMatch(pattern, relBase)
		if err != nil {
			return err
		}
		if !ok && base != displayRoot {
			ok, err = doublestarMatch(pattern, relRoot)
			if err != nil {
				return err
			}
		}
		if ok {
			if len(result.Matches) >= maxMatches {
				result.Truncated = true
				return errStopWalk
			}
			result.Matches = append(result.Matches, relRoot)
		}
		return nil
	})
	if err != nil && err != errStopWalk {
		return "", err
	}
	sort.Strings(result.Matches)
	result.Count = len(result.Matches)
	return encodeSearchResult(result)
}

var errStopWalk = fmt.Errorf("stop walk")

func encodeSearchResult(result any) (string, error) {
	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func scopedPath(root, path string) (string, error) {
	return workspace.ResolveWithin(root, path)
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
