package tui

import (
	"fmt"
	"strings"
	"time"
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

// truncate shortens s to at most n runes, appending "…" if it was cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}

// padRight pads s with spaces to width n (no-op if already wider).
func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
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
