package agent

import (
	"fmt"
	"os"
	"strings"
)

func Preview(tool string, input map[string]any) string {
	switch tool {
	case "write_file":
		path, _ := input["path"].(string)
		content, _ := input["content"].(string)
		return diffAgainstFile(path, content)
	case "edit_file":
		path, _ := input["path"].(string)
		oldStr, _ := input["old_string"].(string)
		newStr, _ := input["new_string"].(string)
		b, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		current := string(b)
		next := strings.Replace(current, oldStr, newStr, 1)
		return diffText(path, current, next)
	default:
		return ""
	}
}

func diffAgainstFile(path, proposed string) string {
	currentBytes, err := os.ReadFile(path)
	if err != nil {
		return newFileDiff(path, proposed)
	}
	return diffText(path, string(currentBytes), proposed)
}

func diffText(path, current, proposed string) string {
	if current == proposed {
		return "(no changes: proposed content is identical to current file)\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s (current)\n", path)
	fmt.Fprintf(&b, "+++ %s (proposed)\n", path)
	oldLines := splitLines(current)
	newLines := splitLines(proposed)
	fmt.Fprintf(&b, "@@ -1,%d +1,%d @@\n", len(oldLines), len(newLines))
	for _, line := range oldLines {
		b.WriteString("-")
		b.WriteString(line)
		b.WriteString("\n")
	}
	for _, line := range newLines {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func newFileDiff(path, content string) string {
	lines := splitLines(content)
	var b strings.Builder
	b.WriteString("--- /dev/null\n")
	fmt.Fprintf(&b, "+++ %s (new file)\n", path)
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, line := range lines {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
