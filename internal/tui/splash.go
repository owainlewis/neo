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
	`    _   ____________`,
	`   / | / / ____/ __ \`,
	`  /  |/ / __/ / / / /`,
	` / /|  / /___/ /_/ /`,
	`/_/ |_/_____/\____/`,
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

	if version := strings.TrimSpace(b.version); version != "" {
		lines = append(lines, "", centerSplashLine(styMuted.Render(version), width))
	}
	lines = append(lines, "")

	inputHint := "@ add files · / commands"
	if lipgloss.Width(inputHint) <= width {
		lines = append(lines, centerSplashLine(styMuted.Render(inputHint), width))
	} else {
		lines = append(lines,
			centerSplashLine(styMuted.Render("@ add files"), width),
			centerSplashLine(styMuted.Render("/ commands"), width),
		)
	}

	workflowHint := "TIP: Press tab to show the workflow while Neo works."
	if lipgloss.Width(workflowHint) <= width {
		lines = append(lines, centerSplashLine(styDim.Render("TIP: ")+styMuted.Render("Press tab to show the workflow while Neo works."), width))
	} else {
		lines = append(lines, centerSplashLine(styMuted.Render("tab → workflow"), width))
	}
	return "\n\n" + strings.Join(lines, "\n")
}

func centerSplashLine(line string, width int) string {
	line = truncate(line, max(width, 1))
	padding := max((width-lipgloss.Width(line))/2, 0)
	return strings.Repeat(" ", padding) + line
}
