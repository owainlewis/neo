// Package workspace locates the project on disk — the repository root and the
// chain of directories from the working directory up to it. Capability modules
// (projectctx, skills) use this to find project-level files without each
// reimplementing the upward walk.
package workspace

import (
	"os"
	"path/filepath"
)

// Ancestors returns the working directory and each parent up to and including
// the repository root — the first ancestor containing a .git entry — or the
// filesystem root when there is no repo. The slice is ordered cwd-first.
func Ancestors(cwd string) []string {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	var dirs []string
	for {
		dirs = append(dirs, dir)
		if isRepoRoot(dir) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached the filesystem root without finding a repo
		}
		dir = parent
	}
	return dirs
}

// Root returns the repository root containing cwd (the first ancestor with a
// .git entry), or cwd's absolute path when no repo is found.
func Root(cwd string) string {
	dirs := Ancestors(cwd)
	if last := dirs[len(dirs)-1]; isRepoRoot(last) {
		return last
	}
	return dirs[0]
}

// isRepoRoot reports whether dir contains a .git entry (directory or file, the
// latter covering git worktrees and submodules).
func isRepoRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
