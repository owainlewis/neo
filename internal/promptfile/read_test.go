package promptfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFile_SizeBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.md")
	for _, size := range []int{MaxBytes - 1, MaxBytes, MaxBytes + 1} {
		if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644); err != nil {
			t.Fatal(err)
		}
		b, err := ReadFile(path)
		if size > MaxBytes {
			if err == nil || len(b) != 0 {
				t.Fatalf("oversized file = %d bytes, %v", len(b), err)
			}
		} else if err != nil || len(b) != size {
			t.Fatalf("size %d = %d bytes, %v", size, len(b), err)
		}
	}
}
