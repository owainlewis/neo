package permission

import "strings"

// Rule is a narrowly-scoped allow decision the user granted during a session.
// For bash it pins a command prefix (the leading tokens); for every other tool
// it matches the tool as a whole.
type Rule struct {
	Tool   string
	Prefix string // bash command prefix tokens, space-joined; empty matches the tool alone
}

// Label is a short, human-friendly description of what the rule grants. It is
// what an "always allow …" prompt shows the user.
func (r Rule) Label() string {
	if r.Prefix != "" {
		return r.Prefix
	}
	return r.Tool
}

func (r Rule) matches(req Request) bool {
	if r.Tool != req.ToolName {
		return false
	}
	if r.Prefix == "" {
		return true
	}
	cmd, _ := stringArg(req.Args, "command")
	// A learned prefix only ever covers a single, plain command. Anything with
	// shell chaining or substitution could smuggle an un-approved command past
	// the grant, so it always falls back to an explicit prompt.
	if !isSimpleCommand(cmd) {
		return false
	}
	return hasTokenPrefix(cmd, r.Prefix)
}

// Allowlist is an ordered set of session rules. The zero value is ready to use.
type Allowlist struct {
	rules []Rule
}

// Allows reports whether any rule already covers the request.
func (a *Allowlist) Allows(req Request) bool {
	// High-risk requests must always get a fresh explicit approval, even if the
	// user previously granted a broad "always allow" rule in this session.
	if explicitApprovalReason(req) != "" {
		return false
	}
	for _, r := range a.rules {
		if r.matches(req) {
			return true
		}
	}
	return false
}

// Add records a rule, ignoring exact duplicates.
func (a *Allowlist) Add(r Rule) {
	for _, existing := range a.rules {
		if existing == r {
			return
		}
	}
	a.rules = append(a.rules, r)
}

// RuleFor derives the rule that answering "always allow" to req should grant.
func RuleFor(req Request) Rule {
	if req.ToolName == "bash" {
		if cmd, ok := stringArg(req.Args, "command"); ok {
			if prefix := commandPrefix(cmd); prefix != "" {
				return Rule{Tool: "bash", Prefix: prefix}
			}
		}
	}
	return Rule{Tool: req.ToolName}
}

// commandPrefix returns the leading one or two tokens of a shell command, which
// is the scope an "always allow" grant pins. Two tokens keeps subcommand-style
// tools (go test, git status, npm run) meaningfully scoped without pinning the
// exact arguments that follow.
func commandPrefix(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	n := 1
	if len(fields) > 1 {
		n = 2
	}
	return strings.Join(fields[:n], " ")
}

func hasTokenPrefix(cmd, prefix string) bool {
	prefFields := strings.Fields(prefix)
	if len(prefFields) == 0 {
		return false
	}
	cmdFields := strings.Fields(cmd)
	if len(cmdFields) < len(prefFields) {
		return false
	}
	for i, p := range prefFields {
		if cmdFields[i] != p {
			return false
		}
	}
	return true
}

// isSimpleCommand reports whether cmd is a single plain command with no shell
// chaining, redirection, or substitution. Prefix grants only apply to these.
func isSimpleCommand(cmd string) bool {
	return !strings.ContainsAny(cmd, "&|;`\n<>") && !strings.Contains(cmd, "$(")
}
