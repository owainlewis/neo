package workflow

import (
	"encoding/json"
	"strings"
)

// failsHeuristic decides whether a step output indicates failure.
//
// Preferred path: a fenced `neo-result` JSON block — this is the structured
// signal the default review step emits and the long-term replacement for
// substring matching (tracked in #19).
//
// Fallback path: legacy prose markers (`verdict: fail` etc.) so prompts that
// don't yet emit the structured block keep working.
func failsHeuristic(output string) bool {
	if status, ok := parseNeoResultStatus(output); ok {
		return strings.EqualFold(status, "fail")
	}

	lower := strings.ToLower(output)
	for _, marker := range []string{
		"verdict: fail",
		"status: fail",
		"result: fail",
		"❌",
		"blocking issues:",
		"tests failed",
	} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

// parseNeoResultStatus extracts the "status" field from a fenced JSON block
// labelled neo-result, if one is present in output. Tolerates trailing
// content and language tag variations (```neo-result, ```neo-result\n).
func parseNeoResultStatus(output string) (string, bool) {
	const tag = "```neo-result"
	start := strings.Index(output, tag)
	if start < 0 {
		return "", false
	}
	rest := output[start+len(tag):]
	// Allow optional newline / whitespace right after the tag.
	rest = strings.TrimLeft(rest, " \t\r\n")
	end := strings.Index(rest, "```")
	if end < 0 {
		return "", false
	}
	blob := strings.TrimSpace(rest[:end])
	var v struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(blob), &v); err != nil {
		return "", false
	}
	if v.Status == "" {
		return "", false
	}
	return v.Status, true
}
