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
	"github.com/owainlewis/neo/internal/llm"
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
	glyph := styAccent.Render("✓")
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
	title := strings.TrimSpace(b.title)
	if title == "" {
		title = "Workflow"
	}
	done, failed, skipped := workflowCounts(b.items)
	total := len(b.items)
	meta := fmt.Sprintf("%d/%d", done+failed+skipped, total)
	sb.WriteString(styAccent.Render(title) + styMuted.Render("  "+meta) + "\n")
	for _, item := range b.items {
		glyph := styMuted.Render("○")
		textStyle := lipgloss.NewStyle()
		switch item.Status {
		case workflow.Running:
			glyph = styTool.Render("●")
			textStyle = styTool
		case workflow.Done:
			glyph = styAccent.Render("✓")
		case workflow.Failed:
			glyph = styErr.Render("✗")
		case workflow.Skipped:
			glyph = styMuted.Render("-")
		}
		line := fmt.Sprintf("%s %s", glyph, textStyle.Render(item.Text))
		if strings.TrimSpace(item.Detail) != "" {
			line += styMuted.Render(" — " + truncate(oneLine(item.Detail), max(width-8, 20)))
		}
		sb.WriteString(line + "\n")
	}
	if total > 0 && done+failed+skipped == total {
		label := "Plan complete"
		if failed > 0 {
			label = "Plan finished with issues"
		}
		sb.WriteString(styMuted.Render(label + fmt.Sprintf(" · %d/%d steps", done+failed+skipped, total)))
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

// treeNode is one step execution in a treeBlock: a node of the supervisor's
// tree, reconstructed in the UI purely from the event stream.
type treeNode struct {
	id, parent int
	step, task string
	startAt    time.Time
	done, ok   bool
	elapsed    time.Duration
	lastLine   string // latest activity while running
}

// treeBlock renders the supervisor subtrees spawned by the chat agent's
// agent calls — subagents and their nested subagents, live:
//
//	● ship  add rate limiting to invites          2m07s
//	├─ ✓ checks                                       4s
//	├─ ● worker  implement limiter middleware     1m12s
//	│     └ bash: just test
//	└─ ✓ verify  branch vs acceptance criteria      31s
//
// Consecutive top-level calls share one block (their trees render as
// siblings); assistant text in between starts a new block. It is a pointer
// block, mutated in place as events arrive.
type treeBlock struct {
	nodes    map[int]*treeNode
	children map[int][]int
	roots    []int
}

func newTreeBlock() *treeBlock {
	return &treeBlock{nodes: map[int]*treeNode{}, children: map[int][]int{}}
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

// render draws the tree as plain styled lines, no background card: mixing
// foreground-styled spans inside a Background style breaks the fill at
// every inner ANSI reset, which reads as patchy off-color blocks.
func (b *treeBlock) render(width int, _ *glamour.TermRenderer) string {
	var sb strings.Builder
	for _, id := range b.roots {
		b.renderNode(&sb, id, "", true, width)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (b *treeBlock) renderNode(sb *strings.Builder, id int, prefix string, last bool, width int) {
	n := b.nodes[id]
	if n == nil {
		return
	}
	connector, childPrefix := "├─ ", prefix+"│  "
	if last {
		connector, childPrefix = "└─ ", prefix+"   "
	}
	if n.parent == 0 { // the chat agent's own calls are the roots
		connector, childPrefix = "", ""
	}

	glyph := styTool.Render("●")
	elapsed := time.Since(n.startAt)
	if n.done {
		elapsed = n.elapsed
		if n.ok {
			glyph = styAccent.Render("✓")
		} else {
			glyph = styErr.Render("✗")
		}
	}
	task := truncate(oneLine(n.task), 44)
	sb.WriteString(fmt.Sprintf("%s%s%s %s %s %s\n",
		prefix, connector, glyph, padRight(n.step, 12), task, styMuted.Render(formatElapsed(elapsed))))
	if !n.done && n.lastLine != "" {
		sb.WriteString(childPrefix + styMuted.Render("  └ "+truncate(oneLine(n.lastLine), max(width-12, 10))) + "\n")
	}
	kids := b.children[id]
	for i, k := range kids {
		b.renderNode(sb, k, childPrefix, i == len(kids)-1, width)
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
