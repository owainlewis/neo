// Package projectctx discovers project-level instruction files (AGENTS.md) and
// composes them into the agent's system prompt as a labelled section.
//
// This is a layered capability, not core behavior: the agent loop works fine
// without it. It is gated by the config feature flag `features.agents_file` and
// wired in at the chat surface (cmd/neo), keeping the core policy-free.
package projectctx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/owainlewis/neo/internal/workspace"
)

// fileName is the instruction file neo looks for. AGENTS.md is the emerging
// cross-tool convention for agent-readable project guidance.
const fileName = "AGENTS.md"

// Doc is one discovered instruction file: where it came from and its contents.
type Doc struct {
	Path    string
	Content string
}

// Load discovers AGENTS.md instruction files for a session rooted at cwd,
// returned in increasing priority (earlier = more general, later = more
// specific):
//
//   - ~/.neo/AGENTS.md                 user-global guidance
//   - AGENTS.md from the repo root down to cwd, outermost first
//
// The upward walk stops at the repository root (the first ancestor containing
// .git) or the filesystem root. Missing or empty files are skipped — only a
// genuine read error (e.g. permissions) is returned.
func Load(cwd string) ([]Doc, error) {
	var docs []Doc

	// User-global, lowest priority.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		d, ok, err := readDoc(filepath.Join(home, ".neo", fileName))
		if err != nil {
			return nil, err
		}
		if ok {
			docs = append(docs, d)
		}
	}

	// Ancestor chain, added outermost → cwd so the most specific file wins by
	// appearing last.
	dirs := workspace.Ancestors(cwd)
	for i := len(dirs) - 1; i >= 0; i-- {
		d, ok, err := readDoc(filepath.Join(dirs[i], fileName))
		if err != nil {
			return nil, err
		}
		if ok {
			docs = append(docs, d)
		}
	}
	return docs, nil
}

// readDoc reads a single instruction file. A missing or whitespace-only file
// yields ok=false with no error; only an unexpected read failure errors.
func readDoc(path string) (doc Doc, ok bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Doc{}, false, nil
		}
		return Doc{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	content := strings.TrimSpace(string(b))
	if content == "" {
		return Doc{}, false, nil
	}
	return Doc{Path: path, Content: content}, true, nil
}

// Augment appends discovered instructions to a base system prompt as a single
// labelled section, each file under its own source-path heading. It returns
// base unchanged when there are no docs, so callers can apply it unconditionally.
func Augment(base string, docs []Doc) string {
	if len(docs) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n# Project instructions\n\n")
	b.WriteString("The following come from AGENTS.md files in this project. ")
	b.WriteString("Treat them as authoritative user guidance for work in this repository.\n")
	for _, d := range docs {
		b.WriteString("\n## ")
		b.WriteString(d.Path)
		b.WriteString("\n\n")
		b.WriteString(d.Content)
		b.WriteString("\n")
	}
	return b.String()
}
