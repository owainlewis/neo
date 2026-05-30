package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// block is one rendered unit in the scrollback.
type block interface {
	render(width int, md *glamour.TermRenderer) string
}

type userBlock struct{ text string }

func (b userBlock) render(width int, _ *glamour.TermRenderer) string {
	prefix := styAccent.Render("›")
	return prefix + " " + b.text
}

type textBlock struct{ text string }

func (b textBlock) render(width int, md *glamour.TermRenderer) string {
	if md == nil {
		return wrap(b.text, width)
	}
	out, err := md.Render(b.text)
	if err != nil {
		return wrap(b.text, width)
	}
	return strings.Trim(out, "\n")
}

type thinkingBlock struct{ text string }

func (b thinkingBlock) render(width int, _ *glamour.TermRenderer) string {
	header := styMuted.Render("▸ thinking")
	body := styThinking.Render(wrap(b.text, width-2))
	return header + "\n" + body
}

type toolCallBlock struct {
	name    string
	args    map[string]any
	startAt time.Time
}

func (b toolCallBlock) render(width int, _ *glamour.TermRenderer) string {
	header, body := toolCardContent(b.name, b.args)
	card := styTool.Render(header)
	if body != "" {
		card += "\n" + styMuted.Render(body)
	}
	return styCardTool.Width(width - 2).Render(card)
}

type toolResultBlock struct {
	name    string
	text    string
	isError bool
	elapsed time.Duration
}

func (b toolResultBlock) render(width int, _ *glamour.TermRenderer) string {
	body := strings.TrimRight(b.text, "\n")
	const maxLines = 12
	lines := strings.Split(body, "\n")
	hidden := 0
	if len(lines) > maxLines {
		hidden = len(lines) - maxLines
		lines = lines[:maxLines]
		body = strings.Join(lines, "\n")
	}
	if strings.TrimSpace(body) == "" {
		body = styMuted.Render("(no output)")
	}
	footerParts := []string{}
	if hidden > 0 {
		footerParts = append(footerParts, fmt.Sprintf("+%d lines", hidden))
	}
	if b.elapsed > 0 {
		footerParts = append(footerParts, fmtElapsed(b.elapsed))
	}
	footer := ""
	if len(footerParts) > 0 {
		footer = "\n" + styMuted.Render(strings.Join(footerParts, " · "))
	}
	style := styCardResult
	if b.isError {
		style = styCardErr
	}
	return style.Width(width - 2).Render(body + footer)
}

type errorBlock struct{ err error }

func (b errorBlock) render(width int, _ *glamour.TermRenderer) string {
	return styErr.Render("! " + b.err.Error())
}

// toolCardContent returns a header line and an optional body for the tool card.
func toolCardContent(name string, args map[string]any) (string, string) {
	switch name {
	case "bash":
		cmd := stringArg(args, "command")
		return "$ " + truncate(oneLine(cmd), 200), ""
	case "read_file":
		return "read " + stringArg(args, "path"), ""
	case "write_file":
		content := stringArg(args, "content")
		lines := strings.Count(content, "\n") + 1
		return "write " + stringArg(args, "path"), fmt.Sprintf("%d lines", lines)
	case "edit_file":
		return "edit " + stringArg(args, "path"), ""
	}
	for k, v := range args {
		if s, ok := v.(string); ok {
			return name, k + "=" + truncate(oneLine(s), 80)
		}
	}
	return name, ""
}

func wrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}
