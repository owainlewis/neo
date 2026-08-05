package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/workspace"
)

const (
	defaultSearchMax     = 200
	maxGrepLineBytes     = 4 * 1024 * 1024
	maxGrepTextBytes     = 16 * 1024
	grepTruncationMarker = "...[truncated]..."
)

var errBinaryFile = errors.New("binary file")

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

func (Grep) ParallelSafe(map[string]any) bool { return true }

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
	root, err := os.OpenRoot(displayRoot)
	if err != nil {
		return "", fmt.Errorf("open grep workspace root %s: %w", displayRoot, err)
	}
	defer root.Close()
	targetName, err := filepath.Rel(displayRoot, target)
	if err != nil {
		return "", fmt.Errorf("resolve grep target %s within workspace root %s: %w", target, displayRoot, err)
	}
	targetName = filepath.ToSlash(targetName)
	contextLines := optInt(input, "context_lines")
	if contextLines < 0 {
		contextLines = 0
	}
	maxMatches := optInt(input, "max_matches")
	if maxMatches <= 0 {
		maxMatches = defaultSearchMax
	}

	files, err := filesUnder(ctx, root, targetName)
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
		lines, err := readTextLines(ctx, root, file.openPath)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				result.Count = len(result.Matches)
				out, encodeErr := encodeSearchResult(result)
				if encodeErr != nil {
					return "", encodeErr
				}
				return out, ctxErr
			}
			if errors.Is(err, errBinaryFile) {
				continue
			}
			return "", fmt.Errorf("grep %s: %w", file.path, err)
		}
		for i, line := range lines {
			if ctxErr := ctx.Err(); ctxErr != nil {
				result.Count = len(result.Matches)
				out, encodeErr := encodeSearchResult(result)
				if encodeErr != nil {
					return "", encodeErr
				}
				return out, ctxErr
			}
			matchLocation := re.FindStringIndex(line)
			if matchLocation == nil {
				continue
			}
			if len(result.Matches) >= maxMatches {
				result.Truncated = true
				result.Count = len(result.Matches)
				return encodeSearchResult(result)
			}
			match := grepMatch{
				Path: file.path,
				Line: i + 1,
				Text: boundedGrepText(line, matchLocation[0]),
			}
			beforeStart := max(0, i-contextLines)
			for n := beforeStart; n < i; n++ {
				match.ContextBefore = append(match.ContextBefore, grepContextLine{Line: n + 1, Text: boundedGrepText(lines[n], -1)})
			}
			afterEnd := min(len(lines), i+contextLines+1)
			for n := i + 1; n < afterEnd; n++ {
				match.ContextAfter = append(match.ContextAfter, grepContextLine{Line: n + 1, Text: boundedGrepText(lines[n], -1)})
			}
			result.Matches = append(result.Matches, match)
		}
	}
	if err := ctx.Err(); err != nil {
		result.Count = len(result.Matches)
		out, encodeErr := encodeSearchResult(result)
		if encodeErr != nil {
			return "", encodeErr
		}
		return out, err
	}
	result.Count = len(result.Matches)
	return encodeSearchResult(result)
}

type Glob struct {
	Root string
}

func (Glob) Name() string { return "glob" }

func (Glob) ParallelSafe(map[string]any) bool { return true }

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

type grepFile struct {
	path     string
	openPath string
}

func filesUnder(ctx context.Context, root *os.Root, path string) ([]grepFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := fs.Stat(root.FS(), path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []grepFile{{path: path, openPath: path}}, nil
	}
	var files []grepFile
	err = fs.WalkDir(root.FS(), path, func(p string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return fmt.Errorf("walk %s: %w", p, err)
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) && p != path {
				return fs.SkipDir
			}
			return nil
		}
		file := grepFile{path: p, openPath: p}
		if d.Type()&fs.ModeSymlink != 0 {
			resolved, err := workspace.ResolveWithin(root.Name(), filepath.Join(root.Name(), filepath.FromSlash(p)))
			if err != nil {
				return fmt.Errorf("resolve discovered path %s within workspace root %s: %w", p, root.Name(), err)
			}
			openPath, err := filepath.Rel(root.Name(), resolved)
			if err != nil {
				return fmt.Errorf("resolve discovered path %s within workspace root %s: %w", p, root.Name(), err)
			}
			file.openPath = filepath.ToSlash(openPath)
		}
		files = append(files, file)
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, err
}

func readTextLines(ctx context.Context, root *os.Root, path string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := root.Open(filepath.FromSlash(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var lines []string
	var line []byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fragment, err := r.ReadSlice('\n')
		if bytes.IndexByte(fragment, 0) >= 0 {
			return nil, errBinaryFile
		}
		fragmentContentBytes := len(fragment)
		if err == nil && fragmentContentBytes > 0 && fragment[fragmentContentBytes-1] == '\n' {
			fragmentContentBytes--
			if fragmentContentBytes > 0 && fragment[fragmentContentBytes-1] == '\r' {
				fragmentContentBytes--
			}
		}
		if fragmentContentBytes > maxGrepLineBytes-len(line) {
			return nil, fmt.Errorf("line %d exceeds grep limit of %d bytes", len(lines)+1, maxGrepLineBytes)
		}
		line = append(line, fragment...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if len(line) > 0 || err == nil {
			line = bytes.TrimSuffix(line, []byte{'\n'})
			line = bytes.TrimSuffix(line, []byte{'\r'})
			lines = append(lines, string(line))
			line = line[:0]
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return lines, nil
}

// boundedGrepText keeps tool output predictable while grep searches the full
// contents of lines within its input limit. A match excerpt is centered on the
// first match; context excerpts retain both ends of the source line.
func boundedGrepText(line string, focus int) string {
	if len(line) <= maxGrepTextBytes {
		return line
	}
	payloadBytes := maxGrepTextBytes - 2*len(grepTruncationMarker)
	if focus < 0 {
		headBytes := payloadBytes / 2
		tailBytes := payloadBytes - headBytes
		return line[:headBytes] + grepTruncationMarker + line[len(line)-tailBytes:]
	}
	start := max(0, focus-payloadBytes/2)
	start = min(start, len(line)-payloadBytes)
	end := start + payloadBytes
	var excerpt strings.Builder
	if start > 0 {
		excerpt.WriteString(grepTruncationMarker)
	}
	excerpt.WriteString(line[start:end])
	if end < len(line) {
		excerpt.WriteString(grepTruncationMarker)
	}
	return excerpt.String()
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
