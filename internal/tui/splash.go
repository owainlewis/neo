package tui

import (
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// splashBlock renders the welcome banner shown once at the top of every
// chat session: a small letter-spaced wordmark inside a thin rounded box,
// metadata on one inline row, and a slash-command hint.
//
// Lives in scrollback so it stays available when the user scrolls back.
type splashBlock struct {
	version string
	model   string
	cwd     string
	branch  string
}

func (b splashBlock) render(width int, _ *glamour.TermRenderer) string {
	mark := lipgloss.NewStyle().
		Foreground(colBanner).
		Bold(true).
		Render("n e o")
	boxed := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBanner).
		Padding(0, 2).
		Render(mark)

	parts := []string{b.version, b.model}
	if b.branch != "" && b.branch != "no-git" {
		parts = append(parts, b.branch)
	}
	parts = append(parts, b.cwd)

	sep := styDim.Render(" · ")
	rendered := make([]string, len(parts))
	for i, p := range parts {
		rendered[i] = styMuted.Render(p)
	}
	metaLine := strings.Join(rendered, sep)

	hint := styDim.Render("type ") + styTool.Render("/help") +
		styDim.Render(" for slash commands")

	var sb strings.Builder
	// Breathing room above the banner.
	sb.WriteString("\n\n")
	sb.WriteString("  " + indent(boxed, "  "))
	sb.WriteString("\n\n  ")
	sb.WriteString(metaLine)
	sb.WriteString("\n\n  ")
	sb.WriteString(hint)
	return sb.String()
}

// indent prepends a prefix to every line after the first; the first line is
// expected to already carry its own leading padding from the caller.
func indent(s, prefix string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
