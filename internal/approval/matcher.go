package approval

import (
	"strings"
	"unicode"
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
		for _, rule := range m.rules {
			if prefix := commandSegments(rule); len(prefix) == 1 && hasWordPrefix(words, prefix[0]) {
				return true
			}
		}
		// Inspect the literal script supplied to the common shell -c wrappers.
		// Other wrappers and shell expansions are deliberately not evaluated.
		if script, ok := shellScript(words); ok {
			if m.requiresCommand(script) {
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
	started, escaped, comment := false, false, false
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
		if comment {
			if ch == '\n' {
				comment = false
				flushSegment()
			}
			continue
		}
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
		case ch == '#' && !started:
			comment = true
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

// Compare decoded words so quoted whitespace does not erase argument boundaries.
func hasWordPrefix(words, prefix []string) bool {
	if len(prefix) == 0 || len(words) < len(prefix) {
		return false
	}
	for i, word := range prefix {
		if words[i] != word {
			return false
		}
	}
	return true
}

// shellScript locates the script after shell invocation options without
// interpreting expansions or reading startup files.
func shellScript(words []string) (string, bool) {
	if len(words) == 0 || (words[0] != "sh" && words[0] != "bash") {
		return "", false
	}
	command := false
	for i := 1; i < len(words); i++ {
		option := words[i]
		if option == "--" {
			if command && i+1 < len(words) {
				return words[i+1], true
			}
			return "", false
		}
		if len(option) < 2 || (option[0] != '-' && option[0] != '+') {
			return option, command
		}
		if strings.HasPrefix(option, "--") {
			if option == "--rcfile" || option == "--init-file" {
				i++ // These options consume a filename, not the command string.
			}
			continue
		}
		for _, flag := range option[1:] {
			if flag == 'c' {
				command = true
			}
			if flag == 'o' || flag == 'O' {
				i++ // Named shell/shopt option.
			}
		}
	}
	return "", false
}
