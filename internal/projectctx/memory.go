package projectctx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const memoryFileName = "memory.md"

// LoadMemory reads the project-local memory file from the repo root.
// Missing or whitespace-only files are treated as absent.
func LoadMemory(root string) (Doc, bool, error) {
	return readDoc(filepath.Join(root, memoryFileName))
}

// MemorySection renders project memory as a distinct system-prompt section.
func MemorySection(doc Doc) string {
	if strings.TrimSpace(doc.Content) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n# Project memory\n\n")
	b.WriteString("The following comes from memory.md in this project. ")
	b.WriteString("Treat it as durable project context that may help in future sessions.\n\n")
	b.WriteString("## ")
	b.WriteString(doc.Path)
	b.WriteString("\n\n")
	b.WriteString(doc.Content)
	b.WriteString("\n")
	return b.String()
}

// AppendMemory appends a human-entered memory entry to the project-local
// memory file, creating it with a short heading when needed.
func AppendMemory(root, text string, now time.Time) (string, error) {
	entry := strings.TrimSpace(text)
	if entry == "" {
		return "", fmt.Errorf("type text after /memory, for example /memory prefers table-driven tests")
	}
	path := filepath.Join(root, memoryFileName)
	body, err := memoryEntryBody(path, entry, now)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

func memoryEntryBody(path, entry string, now time.Time) (string, error) {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	var b strings.Builder
	content := strings.TrimRight(string(existing), "\n")
	if strings.TrimSpace(content) == "" {
		b.WriteString("# Project memory\n\n")
	} else {
		b.WriteString(content)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "- %s: %s\n", now.UTC().Format("2006-01-02"), entry)
	return b.String(), nil
}
