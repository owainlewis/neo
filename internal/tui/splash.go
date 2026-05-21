package tui

import (
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// splashBlock renders the welcome shown once at the top of every chat
// session. Visual model: a left-edge gradient bar carries the brand colour;
// the wordmark, tagline and metadata sit beside it like a magazine
// pull-quote. No borders, no ASCII art — the gradient is the graphical
// element.
type splashBlock struct {
	version string
	model   string
	cwd     string
	branch  string
}

// gradient is the vertical color ramp for the left accent bar — Tailwind
// sky-400 → sky-800. Top is lightest, bottom is deepest. Truecolor; on
// terminals without 24-bit support lipgloss downconverts to the nearest
// 256-color palette automatically.
var gradient = []string{
	"#38bdf8", // sky-400
	"#0ea5e9", // sky-500
	"#0284c7", // sky-600
	"#0369a1", // sky-700
	"#075985", // sky-800
}

const tagline = "a coding agent"

func (b splashBlock) render(width int, _ *glamour.TermRenderer) string {
	// Wordmark: bold true-white so it pops against the muted metadata while
	// the gradient bar carries the colour.
	wordmark := lipgloss.NewStyle().
		Foreground(lipgloss.Color("231")).
		Bold(true).
		Render("NEO")

	sep := styDim.Render("  ·  ")
	pieces := []string{styMuted.Render(b.version), styMuted.Render(b.model)}
	if b.branch != "" && b.branch != "no-git" {
		pieces = append(pieces, styMuted.Render(b.branch))
	}
	metaLine := strings.Join(pieces, sep)
	cwdLine := styMuted.Render(b.cwd)

	// Five content lines aligned to the five-stop gradient bar. The empty
	// middle line creates a visual break between mark and metadata without
	// needing a separator rule.
	content := []string{
		wordmark,
		styMuted.Render(tagline),
		"",
		metaLine,
		cwdLine,
	}

	var sb strings.Builder
	sb.WriteString("\n\n")
	for i, line := range content {
		bar := lipgloss.NewStyle().
			Foreground(lipgloss.Color(gradient[i])).
			Render("█")
		sb.WriteString("   ")
		sb.WriteString(bar)
		sb.WriteString("   ")
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("\n   ")
	sb.WriteString(styDim.Render("type "))
	sb.WriteString(styTool.Render("/help"))
	sb.WriteString(styDim.Render(" for slash commands"))
	return sb.String()
}
