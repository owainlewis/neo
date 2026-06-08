package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const maxToolResultContentBytes = 256 * 1024

func capToolResultContent(out string) string {
	if len(out) <= maxToolResultContentBytes {
		return out
	}

	originalBytes := len(out)
	originalLines := countOutputLines(out)
	shownBytes := maxToolResultContentBytes
	var prefix string
	for {
		marker := toolResultTruncationMarker(originalBytes, originalLines, shownBytes)
		keepBytes := maxToolResultContentBytes - len(marker)
		if keepBytes <= 0 {
			return trimStringBytes(marker, maxToolResultContentBytes)
		}
		nextPrefix := trimStringBytes(out, keepBytes)
		if len(nextPrefix) == shownBytes {
			prefix = nextPrefix
			break
		}
		prefix = nextPrefix
		shownBytes = len(prefix)
	}
	return prefix + toolResultTruncationMarker(originalBytes, originalLines, len(prefix))
}

func toolResultTruncationMarker(originalBytes, originalLines, shownBytes int) string {
	return fmt.Sprintf("\n\n[tool output truncated: original %d bytes across %d lines; showing first %d bytes. Re-run the tool with narrower output to inspect the rest.]", originalBytes, originalLines, shownBytes)
}

func countOutputLines(s string) int {
	if s == "" {
		return 0
	}
	lines := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		lines++
	}
	return lines
}

func trimStringBytes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for len(s) > 0 && !utf8.ValidString(s) {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}
