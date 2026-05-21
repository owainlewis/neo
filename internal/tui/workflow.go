package tui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/workflow"
)

// workflowBlock renders a single workflow run as a Pi-style status widget
// inside the chat scrollback. It is mutable — events from the workflow
// engine are applied via Apply / ApplyAgent and the next viewport refresh
// re-renders the new state.
//
// All mutations are expected to happen on the Bubble Tea Update goroutine
// (events arrive via program.Send), so no synchronisation is needed here.
type workflowBlock struct {
	name      string
	task      string
	round     int
	maxRounds int
	phases    []workflowPhase
	active    int    // index into phases, -1 if none active
	detail    string // current agent activity for the active phase

	startedAt   time.Time
	finishedAt  time.Time
	terminal    workflow.EventKind // WorkflowCompleted, WorkflowFailed, or ""
	failMessage string
}

type phaseStatus int

const (
	phasePending phaseStatus = iota
	phaseActive
	phaseCompleted
	phaseFailed
)

type workflowPhase struct {
	name     string
	status   phaseStatus
	started  time.Time
	finished time.Time
	message  string
}

func newWorkflowBlock(name, task string, phaseNames []string, maxRounds int) *workflowBlock {
	phases := make([]workflowPhase, len(phaseNames))
	for i, n := range phaseNames {
		phases[i] = workflowPhase{name: n, status: phasePending}
	}
	if maxRounds < 1 {
		maxRounds = 1
	}
	return &workflowBlock{
		name:      name,
		task:      task,
		round:     1,
		maxRounds: maxRounds,
		phases:    phases,
		active:    -1,
		startedAt: time.Now(),
	}
}

// Apply mutates the block in response to a workflow-level event.
func (b *workflowBlock) Apply(e workflow.Event) {
	switch e.Kind {
	case workflow.WorkflowStarted:
		if e.Round > 0 {
			b.round = e.Round
		}
	case workflow.PhaseStarted:
		idx := b.phaseIndex(e.Phase)
		if idx < 0 {
			return
		}
		b.phases[idx].status = phaseActive
		b.phases[idx].started = time.Now()
		b.phases[idx].message = ""
		b.active = idx
		if e.Round > 0 {
			b.round = e.Round
		}
		b.detail = ""
	case workflow.PhaseCompleted:
		idx := b.phaseIndex(e.Phase)
		if idx < 0 {
			return
		}
		b.phases[idx].status = phaseCompleted
		b.phases[idx].finished = time.Now()
		if b.active == idx {
			b.active = -1
		}
	case workflow.PhaseFailed:
		idx := b.phaseIndex(e.Phase)
		if idx < 0 {
			return
		}
		b.phases[idx].status = phaseFailed
		b.phases[idx].finished = time.Now()
		b.phases[idx].message = e.Message
		if b.active == idx {
			b.active = -1
		}
	case workflow.RoundRetrying:
		if e.Round > 0 {
			b.round = e.Round
		}
		// Failed phases get reset so the retry round renders fresh.
		for i := range b.phases {
			if b.phases[i].status == phaseFailed {
				b.phases[i] = workflowPhase{name: b.phases[i].name, status: phasePending}
			}
		}
	case workflow.WorkflowCompleted:
		b.terminal = workflow.WorkflowCompleted
		b.finishedAt = time.Now()
		b.active = -1
		b.detail = ""
	case workflow.WorkflowFailed:
		b.terminal = workflow.WorkflowFailed
		b.finishedAt = time.Now()
		b.failMessage = e.Message
		b.active = -1
	}
}

// ApplyAgent updates the detail line based on what the active phase's agent
// is doing. Events for other phases are ignored.
func (b *workflowBlock) ApplyAgent(phaseName string, ev agent.Event) {
	if b.active < 0 || b.phases[b.active].name != phaseName {
		return
	}
	switch ev.Kind {
	case agent.EventToolCall:
		b.detail = toolVerb(ev.Name, ev.Args)
	case agent.EventToolResult:
		b.detail = "" // returns to "thinking"
	}
}

func (b *workflowBlock) phaseIndex(name string) int {
	for i, p := range b.phases {
		if p.name == name {
			return i
		}
	}
	return -1
}

func (b *workflowBlock) render(width int, _ *glamour.TermRenderer) string {
	var sb strings.Builder

	// Header: name + round counter.
	sb.WriteString(styAccent.Render("▸ " + b.name))
	if b.maxRounds > 1 {
		sb.WriteString(styDim.Render(fmt.Sprintf("  round %d/%d", b.round, b.maxRounds)))
	}
	sb.WriteString("\n")
	if b.task != "" {
		limit := width - 4
		if limit < 10 {
			limit = 10
		}
		sb.WriteString(styMuted.Render("  " + truncate(oneLine(b.task), limit)) + "\n")
	}
	sb.WriteString("\n")

	// Phase rows. Pad names to a common column width for alignment.
	nameW := 0
	for _, p := range b.phases {
		if len(p.name) > nameW {
			nameW = len(p.name)
		}
	}
	total := len(b.phases)
	for i, p := range b.phases {
		sb.WriteString("  " + renderPhaseRow(p, i+1, total, nameW, b.detail))
		sb.WriteString("\n")
	}

	// Terminal summary line.
	switch b.terminal {
	case workflow.WorkflowCompleted:
		d := b.finishedAt.Sub(b.startedAt).Round(time.Second)
		sb.WriteString("\n  " + styOK.Render("✓ completed") + styMuted.Render(fmt.Sprintf("  %s", d)))
	case workflow.WorkflowFailed:
		sb.WriteString("\n  " + styErr.Render("✗ failed"))
		if b.failMessage != "" {
			sb.WriteString(styMuted.Render("  " + truncate(oneLine(b.failMessage), 80)))
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

func renderPhaseRow(p workflowPhase, index, total, nameW int, activeDetail string) string {
	glyph, glyphCol := phaseGlyph(p.status)
	glyphStr := lipgloss.NewStyle().Foreground(glyphCol).Render(glyph)

	name := padRight(p.name, nameW+2)
	switch p.status {
	case phasePending:
		name = styDim.Render(name)
	case phaseActive:
		name = lipgloss.NewStyle().Foreground(glyphCol).Render(name)
	}

	counter := styDim.Render(fmt.Sprintf("%d/%d", index, total))

	detail := ""
	switch p.status {
	case phaseCompleted:
		d := p.finished.Sub(p.started)
		if d > 0 {
			detail = "  " + styMuted.Render(fmtElapsed(d.Round(100*time.Millisecond)))
		}
	case phaseFailed:
		if p.message != "" {
			detail = "  " + styErr.Render(truncate(oneLine(p.message), 60))
		}
	case phaseActive:
		if activeDetail != "" {
			detail = "  " + styMuted.Render(truncate(oneLine(activeDetail), 60))
		}
	}

	return fmt.Sprintf("%s %s %s%s", glyphStr, name, counter, detail)
}

func phaseGlyph(s phaseStatus) (string, color.Color) {
	switch s {
	case phaseActive:
		return "▶", colDotThinking
	case phaseCompleted:
		return "✓", colOK
	case phaseFailed:
		return "✗", colErr
	default:
		return "○", colDim
	}
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
