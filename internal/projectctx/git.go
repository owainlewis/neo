package projectctx

import (
	"bytes"
	"os/exec"
	"strings"
)

// LoadGitContext snapshots lightweight git metadata for the current session.
// It degrades silently when cwd is outside a git repo or git is unavailable.
func LoadGitContext(cwd string) (Doc, bool) {
	if strings.TrimSpace(cwd) == "" {
		return Doc{}, false
	}
	if _, err := gitOutput(cwd, "rev-parse", "--show-toplevel"); err != nil {
		return Doc{}, false
	}

	branch, err := gitOutput(cwd, "branch", "--show-current")
	if err != nil {
		return Doc{}, false
	}
	if branch == "" {
		branch = "HEAD"
	}

	status, err := gitOutput(cwd, "status", "--short")
	if err != nil {
		return Doc{}, false
	}
	if status == "" {
		status = "(clean working tree)"
	}

	logText, err := gitOutput(cwd, "log", "--oneline", "-5")
	if err != nil {
		logText = "(no commits yet)"
	}

	var b strings.Builder
	b.WriteString("Branch: ")
	b.WriteString(branch)
	b.WriteString("\n\n")
	b.WriteString("git status --short\n")
	b.WriteString(status)
	b.WriteString("\n\n")
	b.WriteString("git log --oneline -5\n")
	b.WriteString(logText)

	return Doc{Path: cwd, Content: b.String()}, true
}

// GitSection renders git context as a distinct system-prompt section.
func GitSection(doc Doc) string {
	if strings.TrimSpace(doc.Content) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n# Git context\n\n")
	b.WriteString("The following is a lightweight snapshot captured at session start. ")
	b.WriteString("Use it as situational context and expect it to become stale after tool calls.\n\n")
	b.WriteString("## Repository state\n\n")
	b.WriteString(doc.Content)
	b.WriteString("\n")
	return b.String()
}

func gitOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
