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
	for _, rule := range m.rules {
		if rule == tool {
			return true
		}
	}
	if tool != "bash" || len(m.rules) == 0 {
		return false
	}
	command, _ := args["command"].(string)
	return m.requiresCommand(command)
}

func (m Matcher) requiresCommand(command string) bool {
	for _, words := range commandSegments(command) {
		for len(words) > 0 && isAssignment(words[0]) {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		segment := strings.Join(words, " ")
		for _, rule := range m.rules {
			if hasCommandPrefix(segment, strings.Join(strings.Fields(rule), " ")) {
				return true
			}
		}
		// Inspect the literal script supplied to the common shell -c wrappers.
		// Other wrappers and shell expansions are deliberately not evaluated.
		if len(words) >= 3 && (words[0] == "sh" || words[0] == "bash") && words[1] == "-c" {
			if m.requiresCommand(words[2]) {
				return true
			}
		}
	}
	return false
}

// commandSegments splits shell chains and words, preserving separators and
// whitespace inside quotes. It is a small lexical matcher, not a shell parser.
func commandSegments(command string) [][]string {
	var segments [][]string
	var words []string
	var word strings.Builder
	var quote rune
	started, escaped := false, false
	flushWord := func() {
		if started {
			words = append(words, word.String())
			word.Reset()
			started = false
		}
	}
	flushSegment := func() {
		flushWord()
		if len(words) > 0 {
			segments = append(segments, words)
			words = nil
		}
	}
	for _, ch := range command {
		if escaped {
			if ch != '\n' {
				// In double quotes, backslashes only escape shell-special characters.
				if quote == '"' && !strings.ContainsRune("$`\"\\", ch) {
					word.WriteRune('\\')
				}
				word.WriteRune(ch)
				started = true
			}
			escaped = false
			continue
		}
		if ch == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				word.WriteRune(ch)
			}
			continue
		}
		switch {
		case ch == '\'' || ch == '"':
			quote = ch
			started = true
		case ch == '&' || ch == '|' || ch == ';' || ch == '\n':
			flushSegment()
		case unicode.IsSpace(ch):
			flushWord()
		default:
			word.WriteRune(ch)
			started = true
		}
	}
	if escaped {
		word.WriteRune('\\')
		started = true
	}
	flushSegment()
	return segments
}

func isAssignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok || name == "" {
		return false
	}
	for i, ch := range name {
		if ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || i > 0 && ch >= '0' && ch <= '9' {
			continue
		}
		return false
	}
	return true
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
