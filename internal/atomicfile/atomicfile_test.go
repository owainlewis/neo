package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCreatesParentAndSetsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "file.txt")

	if err := Write(path, []byte("hello"), 0o600, 0o700); err != nil {
		t.Fatalf("Write: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("content = %q, want hello", b)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %v, want 0600", got)
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", got)
	}
}

func TestWritePreserveModeKeepsExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := WritePreserveMode(path, []byte("new"), 0o600, 0o700); err != nil {
		t.Fatalf("WritePreserveMode: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(b) != "new" {
		t.Fatalf("content = %q, want new", b)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("file mode = %v, want 0640", got)
	}
}
