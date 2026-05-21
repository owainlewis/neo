package workflow

import "strings"

// failsHeuristic decides whether a phase output indicates failure. This is a
// stopgap — substring matching on prose is fragile (see GH #19) and will be
// replaced with a structured signal (e.g. a fenced JSON tail block or a
// dedicated report tool).
func failsHeuristic(output string) bool {
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
