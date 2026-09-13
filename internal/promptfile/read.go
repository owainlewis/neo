// Package promptfile bounds instruction files before they enter a prompt.
package promptfile

import (
	"fmt"
	"io"
	"os"
)

// MaxBytes is the maximum size of one injected instruction file, including frontmatter.
const MaxBytes = 32 * 1024

// ReadFile reads a user-owned instruction file.
func ReadFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Read(f)
}

// Read reads at most MaxBytes+1 bytes and rejects oversized or non-regular files.
// The caller owns f, which may have been opened through a workspace root.
func Read(f *os.File) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("skipped: not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxBytes {
		return nil, fmt.Errorf("skipped: exceeds %d-byte (32 KiB) instruction limit", MaxBytes)
	}
	return b, nil
}
