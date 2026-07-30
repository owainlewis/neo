package tui

import (
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// splashBlock renders the compact welcome shown once at the top of a session.
// Repository and model context deliberately stay in the footer so the initial
// screen does not repeat itself.
type splashBlock struct {
	version string
}

var neoWordmark = []string{
	` _   _  _____   ___ `,
	`| \ | || ____| / _ \`,
	`|  \| ||  _|  | | | |`,
	`| |\  || |___ | |_| |`,
	`|_| \_||_____| \___/`,
}

func (b splashBlock) render(width int, _ *glamour.TermRenderer) string {
	width = max(width, 1)
	lines := make([]string, 0, len(neoWordmark)+6)
	wordmark := neoWordmark
	for _, line := range neoWordmark {
		if lipgloss.Width(line) > width {
			wordmark = []string{"NEO"}
			break
		}
	}
	for _, line := range wordmark {
		lines = append(lines, centerSplashLine(styAccent.Render(line), width))
	}

	detail := "workflow-first coding agent"
	if version := strings.TrimSpace(b.version); version != "" {
		detail += " · " + version
	}
	lines = append(lines, "", centerSplashLine(styMuted.Render(detail), width), "")

	tip := "TIP: Use @ to add files or / for commands."
	if lipgloss.Width(tip) <= width {
		lines = append(lines, centerSplashLine(styDim.Render("TIP: ")+styMuted.Render("Use @ to add files or / for commands."), width))
	} else {
		lines = append(lines,
			centerSplashLine(styDim.Render("TIP"), width),
			centerSplashLine(styMuted.Render("@ add files"), width),
			centerSplashLine(styMuted.Render("/ commands"), width),
		)
	}
	return "\n\n" + strings.Join(lines, "\n")
}

func centerSplashLine(line string, width int) string {
	line = truncate(line, max(width, 1))
	padding := max((width-lipgloss.Width(line))/2, 0)
	return strings.Repeat(" ", padding) + line
}
