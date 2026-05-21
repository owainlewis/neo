package tui

import (
	"fmt"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// neoBanner is the block-shadow ASCII art shown at the top of every new
// chat session. Kept small (6 lines × ~27 cols) so it doesn't dominate
// short terminals.
var neoBanner = []string{
	`███╗   ██╗███████╗ ██████╗ `,
	`████╗  ██║██╔════╝██╔═══██╗`,
	`██╔██╗ ██║█████╗  ██║   ██║`,
	`██║╚██╗██║██╔══╝  ██║   ██║`,
	`██║ ╚████║███████╗╚██████╔╝`,
	`╚═╝  ╚═══╝╚══════╝ ╚═════╝ `,
}

// splashBlock renders a one-time welcome banner with version + model + cwd
// + a hint about /help. Appended to the model's scrollback on construction
// so it's the first thing the user sees and stays available when they
// scroll back.
type splashBlock struct {
	version string
	model   string
	cwd     string
	branch  string
}

func (b splashBlock) render(width int, _ *glamour.TermRenderer) string {
	bannerStyle := lipgloss.NewStyle().Foreground(colDotThinking).Bold(true)
	var sb strings.Builder
	// A little breathing room above the banner so it doesn't sit flush
	// against the top of the viewport.
	sb.WriteString("\n\n")
	for _, line := range neoBanner {
		sb.WriteString(bannerStyle.Render(line))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	rows := [][2]string{
		{"version", b.version},
		{"model", b.model},
		{"cwd", b.cwd},
	}
	if b.branch != "" && b.branch != "no-git" {
		rows = append(rows, [2]string{"branch", b.branch})
	}

	// Pad labels for clean alignment.
	labelW := 0
	for _, r := range rows {
		if len(r[0]) > labelW {
			labelW = len(r[0])
		}
	}
	for _, r := range rows {
		sb.WriteString(fmt.Sprintf("  %s  %s\n",
			styDim.Render(padRight(r[0], labelW)),
			styMuted.Render(r[1])))
	}

	sb.WriteString("\n  " + styDim.Render("type ") +
		styTool.Render("/help") + styDim.Render(" for slash commands"))

	return sb.String()
}
