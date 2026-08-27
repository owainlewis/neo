package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/owainlewis/neo/internal/atomicfile"
	"github.com/owainlewis/neo/internal/llm"
)

// MaxReadBytes caps the total bytes ReadFile will return in a single call.
// Files larger than this must be paged via offset/limit.
const MaxReadBytes = MaxOutputBytes

type ReadFile struct{}

func (ReadFile) Name() string { return "read_file" }

func (ReadFile) ParallelSafe(map[string]any) bool { return true }

func (ReadFile) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "read_file",
		Description: "Read a file from disk. Each line is prefixed with its 1-indexed line number and a tab, matching the line numbers grep reports. Use offset/limit to page through large files.",
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

func (ReadFile) Run(ctx context.Context, input map[string]any) (string, error) {
	path, err := mustString(input, "path")
	if err != nil {
		return "", err
	}
	return readFileWindow(ctx, path, optInt(input, "offset"), optInt(input, "limit"))
}

// lineNumberPrefix renders the gutter for one line. The tab keeps the content
// column stable and gives the model an unambiguous place to cut when it needs
// the raw text back for edit_file.
func lineNumberPrefix(line int) string {
	return fmt.Sprintf("%6d\t", line)
}

func readFileWindow(ctx context.Context, path string, offset, limit int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	startLine := 1
	if offset > 0 {
		startLine = offset
	}

	var out strings.Builder
	r := bufio.NewReader(f)
	fileEmpty := false
	if info, err := f.Stat(); err == nil {
		fileEmpty = info.Size() == 0
	}
	lineNo := 1
	selected := 0
	wroteLine := false
	inSelectedLine := false
	lastLineEndedWithNewline := false

	for limit <= 0 || selected < limit {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return out.String(), err
			}
		}

		part, err := r.ReadSlice('\n')
		if len(part) > 0 {
			endsLine := part[len(part)-1] == '\n'
			lastLineEndedWithNewline = endsLine
			if endsLine {
				part = part[:len(part)-1]
			}

			if lineNo >= startLine {
				if !inSelectedLine {
					if !wroteLine {
						wroteLine = true
					} else if err := appendReadFileChunk(&out, "\n"); err != nil {
						return "", err
					}
					if err := appendReadFileChunk(&out, lineNumberPrefix(lineNo)); err != nil {
						return "", err
					}
					inSelectedLine = true
				}
				if err := appendReadFileChunk(&out, string(part)); err != nil {
					return "", err
				}
			}

			if endsLine {
				if lineNo >= startLine {
					selected++
					inSelectedLine = false
				}
				lineNo++
			}
		}

		if err == nil {
			continue
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != io.EOF {
			return "", err
		}
		if len(part) > 0 {
			break
		}
		if lastLineEndedWithNewline && lineNo >= startLine && (limit <= 0 || selected < limit) {
			if !wroteLine {
				wroteLine = true
			} else if err := appendReadFileChunk(&out, "\n"); err != nil {
				return "", err
			}
		}
		break
	}

	if offset > 0 && !wroteLine && (!fileEmpty || offset != 1) {
		return "", fmt.Errorf("read_file: offset %d is past end of file", offset)
	}
	return out.String(), nil
}

func appendReadFileChunk(out *strings.Builder, chunk string) error {
	if out.Len()+len(chunk) > MaxReadBytes {
		return fmt.Errorf("read_file: selection exceeds %d bytes; narrow with offset/limit", MaxReadBytes)
	}
	_, _ = out.WriteString(chunk)
	return nil
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
		Description: "Replace exactly one occurrence of old_string with new_string in a file. Fails if old_string is missing or appears more than once. Strip the line-number prefix read_file adds before matching; old_string must be the file's raw text.",
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
	if oldStr == "" {
		return "", fmt.Errorf("edit_file: old_string must not be empty; provide exact unique text to replace")
	}
	newStr, err := mustString(input, "new_string")
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("edit_file: read %s: %w", path, err)
	}
	s := string(b)
	n := strings.Count(s, oldStr)
	if n == 0 {
		return "", fmt.Errorf("edit_file: old_string not found in %s; read the file and retry with exact text", path)
	}
	if n > 1 {
		return "", fmt.Errorf("edit_file: old_string found %d times in %s; include more surrounding text so it is unique", n, path)
	}
	out := strings.Replace(s, oldStr, newStr, 1)
	if err := atomicWrite(path, []byte(out)); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", path), nil
}

func atomicWrite(path string, content []byte) error {
	return atomicfile.WritePreserveMode(path, content, 0o644, 0o755)
}
