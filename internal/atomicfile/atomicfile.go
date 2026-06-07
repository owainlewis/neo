// Package atomicfile provides small helpers for crash-safe file replacement.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write replaces path with b by writing a sibling temp file and renaming it
// into place. Parent directories are created with dirPerm and the replacement
// file is written with perm.
func Write(path string, b []byte, perm, dirPerm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// WritePreserveMode replaces path like Write, preserving the existing file's
// permission bits when it exists and falling back to defaultPerm otherwise.
func WritePreserveMode(path string, b []byte, defaultPerm, dirPerm os.FileMode) error {
	perm := defaultPerm
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	} else if !os.IsNotExist(err) {
		return err
	}
	return Write(path, b, perm, dirPerm)
}
