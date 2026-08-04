// Package projectctx discovers project-level instruction files (AGENTS.md) and
// composes them into the agent's system prompt as a labelled section.
//
// This is a layered capability, not core behavior: the agent loop works fine
// without it. It is gated by the config feature flag `features.agents_file` and
// wired in at the chat surface (cmd/neo), keeping the core policy-free.
package projectctx

import (
	"errors"
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
// .git), or at cwd when there is no repository. Project instruction files must
// resolve within that boundary. Missing or empty files are skipped. Read and
// validation errors are returned alongside any documents that loaded safely.
func Load(cwd string) ([]Doc, error) {
	var docs []Doc
	var loadErrs []error

	// User-global, lowest priority.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		d, ok, err := readDoc(filepath.Join(home, ".neo", fileName))
		if err != nil {
			loadErrs = append(loadErrs, err)
		} else if ok {
			docs = append(docs, d)
		}
	}

	// Ancestor chain, added outermost → cwd so the most specific file wins by
	// appearing last. Unlike the explicitly user-owned global file above,
	// repository files must stay inside the workspace after resolving symlinks.
	root := workspace.Root(cwd)
	dirs := workspace.Ancestors(cwd)
	for len(dirs) > 0 && filepath.Clean(dirs[len(dirs)-1]) != filepath.Clean(root) {
		dirs = dirs[:len(dirs)-1]
	}
	projectRoot, err := os.OpenRoot(root)
	if err != nil {
		loadErrs = append(loadErrs, fmt.Errorf("open workspace root %s: %w", root, err))
		return docs, errors.Join(loadErrs...)
	}
	defer projectRoot.Close()

	for i := len(dirs) - 1; i >= 0; i-- {
		path := filepath.Join(dirs[i], fileName)
		name, err := filepath.Rel(root, path)
		if err != nil {
			loadErrs = append(loadErrs, fmt.Errorf("resolve project instructions %s within workspace root %s: %w", path, root, err))
			continue
		}
		d, ok, err := readRootedDoc(projectRoot, name, path)
		if err != nil {
			loadErrs = append(loadErrs, err)
			continue
		}
		if ok {
			docs = append(docs, d)
		}
	}
	return docs, errors.Join(loadErrs...)
}

// readRootedDoc reads a project instruction through an os.Root so symlink
// validation and the read are one rooted operation. This prevents a repository
// from swapping an accepted symlink to an escaping target between validation
// and use.
func readRootedDoc(root *os.Root, name, sourcePath string) (doc Doc, ok bool, err error) {
	b, err := root.ReadFile(name)
	if err != nil {
		if os.IsNotExist(err) {
			return Doc{}, false, nil
		}
		return Doc{}, false, fmt.Errorf("read project instructions %s: path must resolve within workspace root %s: %w", sourcePath, root.Name(), err)
	}
	content := strings.TrimSpace(string(b))
	if content == "" {
		return Doc{}, false, nil
	}
	return Doc{Path: sourcePath, Content: content}, true, nil
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
