package tui

import (
	"strings"

	"charm.land/glamour/v2"
)

// splashBlock renders the welcome shown once at the top of every chat session.
// It introduces Neo's workflow-first contract, then gives just enough project
// context and input discovery to help the user start useful work.
type splashBlock struct {
	version string
	model   string
	cwd     string
	branch  string
}

func (b splashBlock) render(width int, _ *glamour.TermRenderer) string {
	context := b.cwd
	if b.branch != "" && b.branch != "no-git" {
		context += " · " + b.branch
	}
	backend := strings.TrimSpace(strings.Join(nonEmpty(b.model, b.version), " · "))

	lines := []string{
		styAccent.Render("NEO") + "  " + styLabel.Render("workflow-first coding agent"),
		styMuted.Render("Plans the work, executes it, and verifies the result."),
		"",
		styMuted.Render(context),
	}
	if backend != "" {
		lines = append(lines, styDim.Render(backend))
	}
	lines = append(lines,
		"",
		styDim.Render("Try: ")+styMuted.Render("Fix a failing test and verify the change"),
		styTool.Render("@")+styDim.Render(" add files  ·  ")+styTool.Render("/")+styDim.Render(" commands"),
	)

	for i, line := range lines {
		lines[i] = "  " + truncate(line, max(width-2, 1))
	}
	return "\n\n" + strings.Join(lines, "\n")
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
