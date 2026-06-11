package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// stringArg extracts a string value from a tool-input map by key.
// Returns "" if absent or not a string.
func stringArg(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// oneLine collapses a multi-line string to a single line, replacing newlines
// with " ⏎ " so the content stays readable in status bars and log entries.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	return strings.TrimSpace(s)
}

// truncate shortens s to at most n display cells, appending "…" if it was
// cut. Width is measured in terminal cells (CJK and emoji count as 2), ANSI
// escape sequences are skipped, and cuts land on grapheme-cluster boundaries
// so combining marks stay attached and output is always valid UTF-8. When s
// does not fit and n <= 1 (including n <= 0), the result is just "…".
func truncate(s string, n int) string {
	if ansi.StringWidth(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return ansi.Truncate(s, n, "…")
}

// padRight pads s with spaces to n display cells (no-op if already wider).
func padRight(s string, n int) string {
	w := ansi.StringWidth(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// fmtElapsed formats a duration for display in tool-result and step-row
// elapsed columns.
func fmtElapsed(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("Took %dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("Took %.1fs", d.Seconds())
	default:
		return fmt.Sprintf("Took %s", d.Round(time.Second))
	}
}
