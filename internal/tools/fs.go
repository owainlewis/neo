package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/owainlewis/neo/internal/atomicfile"
	"github.com/owainlewis/neo/internal/llm"
)

// MaxReadBytes caps the total bytes ReadFile will return in a single call.
// Files larger than this must be paged via offset/limit.
const MaxReadBytes = MaxOutputBytes

// fileState remembers what a file looked like when the agent last read it.
// edit_file consults it so a write cannot land on content the agent never saw:
// the user saving in their editor, a git checkout, or a concurrent work-mode
// subagent all change a file without the agent being able to observe it.
//
// A nil *fileState tracks nothing, which keeps the zero-value tools usable.
type fileState struct {
	mu   sync.Mutex
	seen map[string]fileStamp
}

// fileStamp is enough to catch a real external write. Hashing would be exact
// but a stat is free and the difference does not come up in practice.
type fileStamp struct {
	modTime time.Time
	size    int64
}

func newFileState() *fileState { return &fileState{seen: map[string]fileStamp{}} }

func stamp(path string) (fileStamp, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, false
	}
	return fileStamp{modTime: info.ModTime(), size: info.Size()}, true
}

func stateKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// record notes the file's current state as what the agent has seen.
func (s *fileState) record(path string) {
	if s == nil {
		return
	}
	current, ok := stamp(path)
	if !ok {
		return
	}
	s.mu.Lock()
	s.seen[stateKey(path)] = current
	s.mu.Unlock()
}

// changedSinceRead reports whether the file differs from what the agent last
// read. A path the agent has never read is not stale: trusting the model to
// read before it edits is fine, because that is a decision it can make. This
// guard is only for changes it cannot see.
func (s *fileState) changedSinceRead(path string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	previous, tracked := s.seen[stateKey(path)]
	s.mu.Unlock()
	if !tracked {
		return false
	}
	current, ok := stamp(path)
	if !ok {
		return false
	}
	return current != previous
}

type ReadFile struct {
	State *fileState
}

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

func (r ReadFile) Run(ctx context.Context, input map[string]any) (string, error) {
	path, err := mustString(input, "path")
	if err != nil {
		return "", err
	}
	out, err := readFileWindow(ctx, path, optInt(input, "offset"), optInt(input, "limit"))
	if err != nil {
		return "", err
	}
	// A partial read still counts: the stamp records that the agent looked at
	// this file, not that it saw all of it.
	r.State.record(path)
	return out, nil
}

// lineNumberPrefix renders the gutter for one line. The tab keeps the content
// column stable and gives the model an unambiguous place to cut when it needs
// the raw text back for edit_file.
func lineNumberPrefix(line int) string {
	return fmt.Sprintf("%6d\t", line)
}

func readFileWindow(ctx context.Context, path string, offset, limit int) (string, error) {
	// Check before opening: a cancelled turn should report cancellation rather
	// than a filesystem error, and opening a FIFO with no writer blocks.
	if err := ctx.Err(); err != nil {
		return "", err
	}
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

type WriteFile struct {
	State *fileState
}

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

func (w WriteFile) Run(ctx context.Context, input map[string]any) (string, error) {
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
	// Re-stamp so this write does not read as an external change later.
	w.State.record(path)
	return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
}

type EditFile struct {
	State *fileState
}

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

func (e EditFile) Run(ctx context.Context, input map[string]any) (string, error) {
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
	if e.State.changedSinceRead(path) {
		return "", fmt.Errorf("edit_file: %s changed since you read it; read it again and redo the edit against its current contents", path)
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
	// The agent has seen the result of its own edit, so it is not stale.
	e.State.record(path)
	return fmt.Sprintf("edited %s", path), nil
}

func atomicWrite(path string, content []byte) error {
	return atomicfile.WritePreserveMode(path, content, 0o644, 0o755)
}
