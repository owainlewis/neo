package tui

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/workflow"
)

// block is one rendered unit in the scrollback.
type block interface {
	render(width int, md *glamour.TermRenderer) string
}

type userBlock struct{ text string }

func (b userBlock) render(width int, _ *glamour.TermRenderer) string {
	prefix := styAccent.Render("›")
	return prefix + " " + indentContinuation(wrap(b.text, width-2))
}

// indentContinuation keeps wrapped lines aligned under a two-cell prefix.
func indentContinuation(s string) string {
	return strings.ReplaceAll(s, "\n", "\n  ")
}

type textBlock struct{ text string }

// resultSummaryBlock is a compact completion receipt for turns that performed
// visible work. It is intentionally one line so it adds polish without
// stealing focus from the assistant response.
type resultSummaryBlock struct {
	label   string
	detail  string
	elapsed time.Duration
	failed  bool
}

func (b resultSummaryBlock) render(width int, _ *glamour.TermRenderer) string {
	glyph := styOK.Render("✓")
	if b.failed {
		glyph = styWarn.Render("✗")
	}
	parts := []string{strings.TrimSpace(b.label)}
	if strings.TrimSpace(b.detail) != "" {
		parts = append(parts, strings.TrimSpace(b.detail))
	}
	if b.elapsed > 0 {
		parts = append(parts, formatElapsed(b.elapsed))
	}
	line := glyph + " " + strings.Join(parts, styMuted.Render(" · "))
	return styResultSummary.Render(truncate(line, max(width-2, 1)))
}

// workflowBlock is the visible task plan for a multi-step user request. The
// model updates high-level semantic status through the workflow tool; regular
// tool and agent events attach lightweight activity automatically.
type workflowBlock struct {
	title  string
	items  []workflow.Item
	active string
}

func (b *workflowBlock) render(width int, _ *glamour.TermRenderer) string {
	var sb strings.Builder
	title := oneLine(strings.TrimSpace(b.title))
	if title == "" {
		title = "Workflow"
	}
	done, failed, skipped := workflowCounts(b.items)
	total := len(b.items)
	meta := fmt.Sprintf("%d/%d complete", done, total)
	if failed > 0 {
		meta += fmt.Sprintf(" · %d failed", failed)
	}
	if skipped > 0 {
		meta += fmt.Sprintf(" · %d skipped", skipped)
	}
	header := styLabel.Render(title) + styMuted.Render("  "+meta)
	sb.WriteString(truncate(header, max(width, 1)) + "\n")
	for _, item := range b.items {
		glyph := styMuted.Render("○")
		textStyle := styMuted
		switch item.Status {
		case workflow.Running:
			glyph = styTool.Render("●")
			textStyle = styLabel
		case workflow.Done:
			glyph = styOK.Render("✓")
			textStyle = styDim
		case workflow.Failed:
			glyph = styWarn.Render("✗")
			textStyle = styMuted
		case workflow.Skipped:
			glyph = styMuted.Render("-")
			textStyle = styDim
		}
		line := fmt.Sprintf("%s %s", glyph, textStyle.Render(oneLine(item.Text)))
		if strings.TrimSpace(item.Detail) != "" {
			line += styDim.Render("  " + truncate(oneLine(item.Detail), max(width-8, 20)))
		}
		sb.WriteString(truncate(line, max(width, 1)) + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func workflowCounts(items []workflow.Item) (done, failed, skipped int) {
	for _, item := range items {
		switch item.Status {
		case workflow.Done:
			done++
		case workflow.Failed:
			failed++
		case workflow.Skipped:
			skipped++
		}
	}
	return done, failed, skipped
}

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
	body := strings.ReplaceAll(wrap(b.text, width-2), "\n", "\n  ")
	return "  " + styThinking.Render(body)
}

type toolCallBlock struct {
	name    string
	args    map[string]any
	startAt time.Time
	elapsed time.Duration
	// verbose selects full tool-card rendering. When false (the default),
	// the block renders as a single concise status line instead.
	verbose bool
}

func (b toolCallBlock) render(width int, _ *glamour.TermRenderer) string {
	if !b.verbose {
		// Routine successes form an activity trail, not a checklist. Keep them
		// quiet so green checks remain meaningful for workflow and turn
		// completion.
		line := styDim.Render("·") + " " + styMuted.Render(toolReceiptLine(b.name, b.args))
		if b.elapsed > 0 {
			line += styDim.Render("  " + formatElapsed(b.elapsed))
		}
		return truncate(line, max(width, 1))
	}
	header, body := toolCardContent(b.name, b.args)
	card := styTool.Render(header)
	if body != "" {
		card += "\n" + styMuted.Render(body)
	}
	return styCardTool.Width(width - 2).Render(card)
}

// toolReceiptLine renders a concise, past-tense record of completed work.
// Present-tense activity belongs in the live status row, so history never
// leaves finished calls looking as though they are still running.
func toolReceiptLine(name string, args map[string]any) string {
	switch name {
	case "bash":
		return "Ran " + truncate(oneLine(stringArg(args, "command")), 80)
	case "read_file":
		return "Read " + shortPath(stringArg(args, "path"))
	case "write_file":
		return "Wrote " + shortPath(stringArg(args, "path"))
	case "edit_file":
		return "Edited " + shortPath(stringArg(args, "path"))
	case "grep":
		return "Searched " + truncate(oneLine(stringArg(args, "pattern")), 60)
	case "glob":
		return "Matched " + truncate(oneLine(stringArg(args, "pattern")), 60)
	case "agent":
		return "Delegated " + truncate(oneLine(stringArg(args, "prompt")), 60)
	}
	return "Used " + name
}

type toolResultBlock struct {
	name     string
	text     string
	isError  bool
	elapsed  time.Duration
	expanded bool
}

const toolResultMaxLines = 12

func (b toolResultBlock) render(width int, _ *glamour.TermRenderer) string {
	body := strings.TrimRight(b.text, "\n")
	lines := strings.Split(body, "\n")
	hidden := 0
	if len(lines) > toolResultMaxLines && !b.expanded {
		hidden = len(lines) - toolResultMaxLines
		lines = lines[:toolResultMaxLines]
		body = strings.Join(lines, "\n")
	}
	if strings.TrimSpace(body) == "" {
		body = styMuted.Render("(no output)")
	}
	footerParts := []string{}
	if hidden > 0 {
		footerParts = append(footerParts, fmt.Sprintf("+%d lines", hidden), "ctrl+o to expand")
	} else if b.expanded && b.isTruncated() {
		footerParts = append(footerParts, "expanded", "ctrl+o to collapse")
	}
	if b.elapsed > 0 {
		footerParts = append(footerParts, fmtElapsed(b.elapsed))
	}
	footer := ""
	if len(footerParts) > 0 {
		footer = "\n" + styMuted.Render(strings.Join(footerParts, " · "))
	}
	card := styCardResult.Width(width - 2).Render(body + footer)
	if b.isError {
		card = styCardErr.Width(max(width-4, 1)).Render(body + footer)
		return accentCard(card, colWarn)
	}
	return card
}

func (b toolResultBlock) isTruncated() bool {
	body := strings.TrimRight(b.text, "\n")
	return len(strings.Split(body, "\n")) > toolResultMaxLines
}

// runStepOK reads the {"ok":…} envelope on the first line of an agent
// tool result. The tool returns ok=false inside the payload (with no tool
// error) when a step fails, times out, or is denied.
func runStepOK(text string) bool {
	line, _, _ := strings.Cut(text, "\n")
	var env struct {
		Ok bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(line), &env); err == nil {
		return env.Ok
	}
	return true // unrecognized payload: don't paint a false failure
}

// noticeBlock is a quiet one-line status note (e.g. an applied skill).
type noticeBlock struct{ text string }

func (b noticeBlock) render(width int, _ *glamour.TermRenderer) string {
	return styMuted.Render("· " + indentContinuation(wrap(b.text, width-2)))
}

type errorBlock struct{ err error }

func (b errorBlock) render(width int, _ *glamour.TermRenderer) string {
	return styErr.Render("! " + indentContinuation(wrap(b.err.Error(), width-2)))
}

type maxTurnsBlock struct {
	limit int
	label string
}

func (b maxTurnsBlock) render(width int, _ *glamour.TermRenderer) string {
	prefix := "Paused"
	if strings.TrimSpace(b.label) != "" {
		prefix = strings.TrimSpace(b.label) + " paused"
	}
	msg := prefix + " after reaching Neo's safety step limit. Reply to continue."
	if b.limit > 0 {
		msg = fmt.Sprintf("%s after %d steps. Reply to continue.", prefix, b.limit)
	}
	return accentCard(styMuted.Render(msg), colWarn)
}

type approvalBlock struct {
	req agent.ApprovalRequest
}

func (b approvalBlock) render(width int, _ *glamour.TermRenderer) string {
	head, detail := toolCardContent(b.req.ToolName, b.req.Args)
	var sb strings.Builder
	sb.WriteString(styAccent.Render("approval required"))
	sb.WriteString("\n")
	sb.WriteString(styTool.Render(head))
	if detail != "" {
		sb.WriteString("\n")
		sb.WriteString(styMuted.Render(detail))
	}
	sb.WriteString("\n")
	sb.WriteString(styMuted.Render("keys: y approve · n/esc deny"))
	return accentCard(sb.String(), colApprove)
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

// accentCard renders content with a colored left stripe and a one-space gutter,
// the motif used for attention blocks (approval, limits). It draws no
// background fill, so it stays light against the scrollback.
func accentCard(content string, barColor color.Color) string {
	bar := lipgloss.NewStyle().Foreground(barColor).Render("▌")
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = bar + " " + line
	}
	return strings.Join(lines, "\n")
}

func wrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}
