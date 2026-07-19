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
	return prefix + " " + b.text
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
		glyph = styErr.Render("✗")
	}
	parts := []string{strings.TrimSpace(b.label)}
	if strings.TrimSpace(b.detail) != "" {
		parts = append(parts, strings.TrimSpace(b.detail))
	}
	if b.elapsed > 0 {
		parts = append(parts, formatElapsed(b.elapsed))
	}
	line := glyph + " " + strings.Join(parts, styMuted.Render(" · "))
	return styResultSummary.Width(width - 2).Render(line)
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
	meta := fmt.Sprintf("%d/%d", done+failed+skipped, total)
	header := styLabel.Render(title) + styMuted.Render("  "+meta)
	sb.WriteString(truncate(header, max(width, 1)) + "\n")
	for _, item := range b.items {
		glyph := styMuted.Render("○")
		textStyle := lipgloss.NewStyle()
		switch item.Status {
		case workflow.Running:
			glyph = styTool.Render("●")
			textStyle = styLabel
		case workflow.Done:
			glyph = styOK.Render("✓")
		case workflow.Failed:
			glyph = styErr.Render("✗")
		case workflow.Skipped:
			glyph = styMuted.Render("-")
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
	style := styCardResult
	if b.isError {
		style = styCardErr
	}
	return style.Width(width - 2).Render(body + footer)
}

func (b toolResultBlock) isTruncated() bool {
	body := strings.TrimRight(b.text, "\n")
	return len(strings.Split(body, "\n")) > toolResultMaxLines
}

// treeNode is one subagent execution reconstructed from the event stream.
type treeNode struct {
	id       int
	task     string
	startAt  time.Time
	done, ok bool
	elapsed  time.Duration
	lastLine string // latest activity while running
}

// treeBlock renders consecutive subagents spawned by the chat agent:
//
//	● agent  add rate limiting to invites          2m07s
//	✓ agent  verify branch vs acceptance criteria     31s
//
// Consecutive calls share one block; assistant text in between starts a new
// block. It is a pointer block, mutated in place as events arrive.
type treeBlock struct {
	nodes map[int]*treeNode
	roots []int
}

func newTreeBlock() *treeBlock {
	return &treeBlock{nodes: map[int]*treeNode{}}
}

// running reports whether any node in the block is still in flight.
func (b *treeBlock) running() bool {
	for _, n := range b.nodes {
		if !n.done {
			return true
		}
	}
	return false
}

// render draws the activity as plain styled lines, no background card: mixing
// foreground-styled spans inside a Background style breaks the fill at
// every inner ANSI reset, which reads as patchy off-color blocks.
func (b *treeBlock) render(width int, _ *glamour.TermRenderer) string {
	var sb strings.Builder
	for _, id := range b.roots {
		b.renderNode(&sb, id, width)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (b *treeBlock) renderNode(sb *strings.Builder, id int, width int) {
	n := b.nodes[id]
	if n == nil {
		return
	}
	glyph := styTool.Render("●")
	elapsed := time.Since(n.startAt)
	if n.done {
		elapsed = n.elapsed
		if n.ok {
			glyph = styOK.Render("✓")
		} else {
			glyph = styErr.Render("✗")
		}
	}
	task := styMuted.Render(truncate(oneLine(n.task), 44))
	sb.WriteString(fmt.Sprintf("%s %s %s %s\n",
		glyph, styLabel.Render(padRight("agent", 12)), task, styDim.Render(formatElapsed(elapsed))))
	if !n.done && n.lastLine != "" {
		sb.WriteString(styDim.Render("  └ "+truncate(oneLine(n.lastLine), max(width-12, 10))) + "\n")
	}
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
	return styMuted.Render("· " + b.text)
}

type errorBlock struct{ err error }

func (b errorBlock) render(width int, _ *glamour.TermRenderer) string {
	return styErr.Render("! " + b.err.Error())
}

type maxTurnsBlock struct{ limit int }

func (b maxTurnsBlock) render(width int, _ *glamour.TermRenderer) string {
	msg := "Paused after reaching Neo's safety step limit. Reply to continue."
	if b.limit > 0 {
		msg = fmt.Sprintf("Paused after %d steps. Reply to continue.", b.limit)
	}
	return accentCard(styMuted.Render(msg), colWarn)
}

type approvalBlock struct {
	req      agent.ApprovalRequest
	expanded bool
}

func (b approvalBlock) render(width int, _ *glamour.TermRenderer) string {
	head, detail := toolCardContent(b.req.ToolName, b.req.Args)
	var sb strings.Builder
	sb.WriteString(styAccent.Render("permission required"))
	sb.WriteString("\n")
	sb.WriteString(styTool.Render(head))
	if detail != "" {
		sb.WriteString("\n")
		sb.WriteString(styMuted.Render(detail))
	}
	if strings.TrimSpace(b.req.Reason) != "" {
		sb.WriteString("\n")
		sb.WriteString(styMuted.Render("reason: " + b.req.Reason))
	}
	sb.WriteString("\n")
	sb.WriteString(styMuted.Render("keys: y approve · a always allow · n/esc deny · ctrl+o expand preview"))
	if b.req.Preview != "" {
		sb.WriteString("\n\n")
		sb.WriteString(styMuted.Render(trimApprovalPreview(b.req.Preview, b.expanded)))
	}
	return accentCard(sb.String(), colApprove)
}

const approvalPreviewMaxLines = 18

func trimApprovalPreview(preview string, expanded bool) string {
	preview = strings.TrimRight(preview, "\n")
	if preview == "" {
		return ""
	}
	lines := strings.Split(preview, "\n")
	if len(lines) <= approvalPreviewMaxLines {
		return preview
	}
	if expanded {
		return preview + "\n(full preview · ctrl+o to collapse)"
	}
	hidden := len(lines) - approvalPreviewMaxLines
	kept := append([]string(nil), lines[:approvalPreviewMaxLines]...)
	kept = append(kept, fmt.Sprintf("... %d more lines hidden. ctrl+o to inspect before deciding.", hidden))
	return strings.Join(kept, "\n")
}

func approvalPreviewIsTruncated(preview string) bool {
	preview = strings.TrimRight(preview, "\n")
	if preview == "" {
		return false
	}
	return len(strings.Split(preview, "\n")) > approvalPreviewMaxLines
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
