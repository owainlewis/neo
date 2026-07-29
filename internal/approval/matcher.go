package approval

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Matcher reports whether an interactive tool call needs user confirmation.
// Rules are literal user preferences, not a security policy.
type Matcher struct {
	rules []string
}

func New(rules []string) Matcher {
	return Matcher{rules: append([]string(nil), rules...)}
}

func (m Matcher) Requires(tool string, args map[string]any) bool {
	command, _ := args["command"].(string)
	command = strings.TrimLeftFunc(command, unicode.IsSpace)

	for _, rule := range m.rules {
		if rule == tool || tool == "bash" && hasCommandPrefix(command, rule) {
			return true
		}
	}
	return false
}

func hasCommandPrefix(command, prefix string) bool {
	if command == prefix {
		return true
	}
	if !strings.HasPrefix(command, prefix) {
		return false
	}
	next, _ := utf8.DecodeRuneInString(command[len(prefix):])
	return unicode.IsSpace(next)
}
