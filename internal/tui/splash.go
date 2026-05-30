package tui

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// splashBlock renders the welcome shown once at the top of every chat
// session. Visual model: a left-edge gradient bar carries the brand
// colour; the wordmark, tagline and a stacked metadata list sit beside
// it like a magazine pull-quote.
type splashBlock struct {
	version string
	model   string
	cwd     string
	branch  string
	tagline string // motivational line under the wordmark; picked once per session
}

// skyPalette is the Tailwind sky color ramp (light → dark) used by the
// left accent bar. gradientFor picks N equally-spaced stops from this so
// the bar can stretch to match a variable number of content lines.
var skyPalette = []string{
	"#bae6fd", // sky-200
	"#7dd3fc", // sky-300
	"#38bdf8", // sky-400
	"#0ea5e9", // sky-500
	"#0284c7", // sky-600
	"#0369a1", // sky-700
	"#075985", // sky-800
	"#0c4a6e", // sky-900
}

// taglines are short, capitalized motivational lines shown under the wordmark.
// One is chosen per session (see randomTagline) so it stays stable across
// viewport refreshes.
var taglines = []string{
	"Let's build something great",
	"Make it work, then make it right",
	"One small step at a time",
	"Code with intention",
	"Less, but better",
	"Think. Build. Verify.",
	"Today, we make it work",
	"Clarity over cleverness",
	"Small steps, solid ground",
	"Trust the process",
	"Ship it",
	"Onward",
}

func randomTagline() string {
	return taglines[rand.IntN(len(taglines))]
}

func (b splashBlock) render(width int, _ *glamour.TermRenderer) string {
	wordmark := lipgloss.NewStyle().
		Foreground(lipgloss.Color("231")).
		Bold(true).
		Render("NEO")

	// Metadata rendered as a stacked list with aligned labels. Branch row
	// is suppressed when there's no git context.
	rows := [][2]string{
		{"version", b.version},
		{"model", b.model},
	}
	if b.branch != "" && b.branch != "no-git" {
		rows = append(rows, [2]string{"branch", b.branch})
	}
	rows = append(rows, [2]string{"cwd", b.cwd})

	labelW := 0
	for _, r := range rows {
		if len(r[0]) > labelW {
			labelW = len(r[0])
		}
	}
	metaLines := make([]string, 0, len(rows))
	for _, r := range rows {
		metaLines = append(metaLines, fmt.Sprintf("%s  %s",
			styDim.Render(padRight(r[0], labelW)),
			styMuted.Render(r[1])))
	}

	// Compose the full content column: wordmark, tagline, breathing
	// space, then the metadata list.
	tagline := b.tagline
	if tagline == "" {
		tagline = taglines[0]
	}
	content := []string{wordmark, styMuted.Render(tagline), ""}
	content = append(content, metaLines...)

	gradient := gradientFor(len(content))

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

// gradientFor returns n hex colors picked across skyPalette (light → dark),
// so the bar can scale with the content height without losing the ramp.
func gradientFor(n int) []string {
	if n <= 1 {
		return []string{skyPalette[0]}
	}
	last := len(skyPalette) - 1
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = skyPalette[i*last/(n-1)]
	}
	return out
}
