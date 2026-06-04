package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/owainlewis/neo/internal/llm"
)

// MaxReadBytes caps the total bytes ReadFile will return in a single call.
// Files larger than this must be paged via offset/limit.
const MaxReadBytes = 256 * 1024

type ReadFile struct{}

func (ReadFile) Name() string { return "read_file" }

func (ReadFile) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "read_file",
		Description: "Read a file from disk. Returns up to ~256KB. Use offset/limit (1-indexed line numbers) to page through larger files.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "Absolute or relative path"},
				"offset": map[string]any{"type": "integer", "description": "1-indexed starting line (optional)"},
				"limit":  map[string]any{"type": "integer", "description": "Maximum number of lines to return (optional)"},
			},
			"required": []string{"path"},
		},
	}
}

func (ReadFile) Run(_ context.Context, input map[string]any) (string, error) {
	path, err := mustString(input, "path")
	if err != nil {
		return "", err
	}
	offset := optInt(input, "offset")
	limit := optInt(input, "limit")

	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	// Fast path: no pagination requested and the file is small enough.
	if offset <= 0 && limit <= 0 && len(b) <= MaxReadBytes {
		return string(b), nil
	}

	lines := strings.Split(string(b), "\n")
	start := 0
	if offset > 0 {
		start = offset - 1
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	out := strings.Join(lines[start:end], "\n")
	if len(out) > MaxReadBytes {
		return "", fmt.Errorf("read_file: selection exceeds %d bytes; narrow with offset/limit", MaxReadBytes)
	}
	return out, nil
}

type WriteFile struct{}

func (WriteFile) Name() string { return "write_file" }

func (WriteFile) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "write_file",
		Description: "Write content to a file, creating parent directories. Overwrites if exists.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (WriteFile) Run(ctx context.Context, input map[string]any) (string, error) {
	path, err := mustString(input, "path")
	if err != nil {
		return "", err
	}
	content, err := mustString(input, "content")
	if err != nil {
		return "", err
	}
	if err := atomicWrite(path, []byte(content)); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
}

type EditFile struct{}

func (EditFile) Name() string { return "edit_file" }

func (EditFile) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "edit_file",
		Description: "Replace exactly one occurrence of old_string with new_string in a file. Fails if old_string is missing or appears more than once.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string"},
				"old_string": map[string]any{"type": "string"},
				"new_string": map[string]any{"type": "string"},
			},
			"required": []string{"path", "old_string", "new_string"},
		},
	}
}

func (EditFile) Run(ctx context.Context, input map[string]any) (string, error) {
	path, err := mustString(input, "path")
	if err != nil {
		return "", err
	}
	oldStr, err := mustString(input, "old_string")
	if err != nil {
		return "", err
	}
	newStr, err := mustString(input, "new_string")
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := string(b)
	n := strings.Count(s, oldStr)
	if n == 0 {
		return "", fmt.Errorf("old_string not found in %s", path)
	}
	if n > 1 {
		return "", fmt.Errorf("old_string found %d times in %s; needs to be unique", n, path)
	}
	out := strings.Replace(s, oldStr, newStr, 1)
	if err := atomicWrite(path, []byte(out)); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", path), nil
}

// atomicWrite writes content via a sibling temp file + rename, so a crash
// mid-write cannot leave a half-written file at path.
func atomicWrite(path string, content []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	} else if !os.IsNotExist(err) {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".neo-write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
