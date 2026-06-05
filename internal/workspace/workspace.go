// Package workspace locates the project on disk — the repository root and the
// chain of directories from the working directory up to it. Capability modules
// (projectctx, skills) use this to find project-level files without each
// reimplementing the upward walk.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// ResolveWithin returns path resolved against root, following symlinks enough
// to prove the final target stays under root. The path itself does not need to
// exist; the nearest existing parent is resolved and the missing suffix is
// appended to that real parent.
func ResolveWithin(root, path string) (string, error) {
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve root %s: %w", absRoot, err)
	}
	if path == "" {
		path = absRoot
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(absRoot, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	realPath, err := resolveExistingPrefix(absPath)
	if err != nil {
		return "", err
	}
	realRoot = filepath.Clean(realRoot)
	realPath = filepath.Clean(realPath)
	if realPath == realRoot {
		return realPath, nil
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside workspace root %s", path, realRoot)
	}
	return realPath, nil
}

func resolveExistingPrefix(path string) (string, error) {
	path = filepath.Clean(path)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real, nil
	}
	for dir := path; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(dir); err == nil {
			realDir, err := filepath.EvalSymlinks(dir)
			if err != nil {
				return "", err
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return "", err
			}
			if rel == "." {
				return realDir, nil
			}
			return filepath.Join(realDir, rel), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no existing parent for %s", path)
		}
	}
}
