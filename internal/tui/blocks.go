package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/llm"
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

// noticeBlock is a quiet one-line status note (e.g. an applied skill).
type noticeBlock struct{ text string }

func (b noticeBlock) render(width int, _ *glamour.TermRenderer) string {
	return styMuted.Render("· " + b.text)
}

type errorBlock struct{ err error }

func (b errorBlock) render(width int, _ *glamour.TermRenderer) string {
	return styErr.Render("! " + b.err.Error())
}

type maxTurnsBlock struct{ limit int }

func (b maxTurnsBlock) render(width int, _ *glamour.TermRenderer) string {
	msg := "hit turn limit. Reply to continue."
	if b.limit > 0 {
		msg = fmt.Sprintf("hit turn limit (%d). Reply to continue.", b.limit)
	}
	return styCardWarn.Width(width - 2).Render(msg)
}

type toolsBlock struct {
	specs []llm.ToolSpec
}

func (b toolsBlock) render(width int, _ *glamour.TermRenderer) string {
	var sb strings.Builder
	sb.WriteString(styAccent.Render("tools") + "\n")
	for _, spec := range b.specs {
		sb.WriteString(fmt.Sprintf("  %s  %s\n", styTool.Render(padRight(spec.Name, 12)), styMuted.Render(spec.Description)))
	}
	return strings.TrimRight(sb.String(), "\n")
}

type tokensBlock struct {
	usage llm.Usage
}

func (b tokensBlock) render(width int, _ *glamour.TermRenderer) string {
	lines := []string{
		fmt.Sprintf("input: %d", b.usage.InputTokens),
		fmt.Sprintf("output: %d", b.usage.OutputTokens),
		fmt.Sprintf("cache write: %d", b.usage.CacheCreationTokens),
		fmt.Sprintf("cache read: %d", b.usage.CacheReadTokens),
	}
	return styCardResult.Width(width - 2).Render(strings.Join(lines, "\n"))
}

type approvalBlock struct {
	req agent.ApprovalRequest
}

func (b approvalBlock) render(width int, _ *glamour.TermRenderer) string {
	var sb strings.Builder
	sb.WriteString("approve ")
	sb.WriteString(b.req.ToolName)
	sb.WriteString("?  y / n")
	if b.req.Preview != "" {
		sb.WriteString("\n")
		sb.WriteString(trimApprovalPreview(b.req.Preview))
	}
	return styCardWarn.Width(width - 2).Render(sb.String())
}

const approvalPreviewMaxLines = 18

func trimApprovalPreview(preview string) string {
	preview = strings.TrimRight(preview, "\n")
	if preview == "" {
		return ""
	}
	lines := strings.Split(preview, "\n")
	if len(lines) <= approvalPreviewMaxLines {
		return preview
	}
	hidden := len(lines) - approvalPreviewMaxLines
	kept := append([]string(nil), lines[:approvalPreviewMaxLines]...)
	kept = append(kept, fmt.Sprintf("... %d more lines hidden. Approve to apply the full change.", hidden))
	return strings.Join(kept, "\n")
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
	case "grep":
		target := stringArg(args, "path")
		if target == "" {
			target = "."
		}
		return "grep " + truncate(oneLine(stringArg(args, "pattern")), 120), target
	case "glob":
		return "glob " + truncate(oneLine(stringArg(args, "pattern")), 120), stringArg(args, "path")
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
