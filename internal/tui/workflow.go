package tui

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/artifact"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/phase"
	"github.com/owainlewis/neo/internal/workflow"
)

// WorkflowConfig carries everything the TUI needs to construct and run a
// workflow.Engine in response to a /run slash command.
type WorkflowConfig struct {
	Config *config.Config
	Runner *phase.Runner
	Store  *artifact.Store
}

// teaSendFn matches the signature of tea.Program.Send. Extracted so tests can
// pass a simple capture function without instantiating a real program.
type teaSendFn func(tea.Msg)

// tuiSink converts workflow.Engine events into Bubble Tea messages so they
// land on the Update goroutine and mutate the active workflowBlock safely.
type tuiSink struct {
	send teaSendFn
}

func (s *tuiSink) OnWorkflow(e workflow.Event) {
	s.send(workflowEventMsg{ev: e})
}

func (s *tuiSink) OnAgent(stepName string, ev agent.Event) {
	s.send(workflowAgentEventMsg{step: stepName, ev: ev})
}

// Messages routed between the workflow goroutine and the Bubble Tea program.
type workflowEventMsg struct{ ev workflow.Event }
type workflowAgentEventMsg struct {
	step string
	ev   agent.Event
}
type workflowDoneMsg struct{ err error }

// definitionFor returns the workflow.Definition for a flow by name from the
// loaded config, or an error if the flow doesn't exist.
func (c WorkflowConfig) definitionFor(name string) (workflow.Definition, error) {
	if c.Config == nil {
		return workflow.Definition{}, fmt.Errorf("no config loaded")
	}
	fc, ok := c.Config.Flows[name]
	if !ok {
		return workflow.Definition{}, fmt.Errorf("no flow %q in config (%s)", name, c.Config.Source())
	}
	return workflow.Definition{
		Name:      name,
		Steps:     fc.Steps,
		RetryFrom: fc.RetryFrom,
		MaxRounds: fc.MaxRounds,
	}, nil
}

// launchWorkflow constructs an engine for the given def and runs it in a
// goroutine. The returned cancel function aborts the run; engine events
// flow back to the program via the sink. When Run returns, a workflowDoneMsg
// is sent so the model can clear its active state.
func (c WorkflowConfig) launchWorkflow(ctx context.Context, send teaSendFn, def workflow.Definition, task string) context.CancelFunc {
	runCtx, cancel := context.WithCancel(ctx)
	sink := &tuiSink{send: send}
	eng := &workflow.Engine{
		Resolver: c.Config,
		Runner:   c.Runner,
		Store:    c.Store,
		Sink:     sink,
	}
	go func() {
		err := eng.Run(runCtx, def, task)
		send(workflowDoneMsg{err: err})
	}()
	return cancel
}

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
	steps     []workflowStep
	active    int    // index into steps, -1 if none active
	detail    string // current agent activity for the active step

	startedAt   time.Time
	finishedAt  time.Time
	terminal    workflow.EventKind // WorkflowCompleted, WorkflowFailed, or ""
	failMessage string
}

type stepStatus int

const (
	stepPending stepStatus = iota
	stepActive
	stepCompleted
	stepFailed
)

type workflowStep struct {
	name     string
	status   stepStatus
	started  time.Time
	finished time.Time
	message  string
	// activity holds the most recent tool calls for this step, head=newest.
	// Used to render a persistent log under the active row so fast tool
	// calls don't flash past unreadably.
	activity []activityEntry
}

const activityCap = 3

type activityEntry struct {
	desc       string
	startedAt  time.Time
	finishedAt time.Time // zero if still running
}

func newWorkflowBlock(name, task string, stepNames []string, maxRounds int) *workflowBlock {
	steps := make([]workflowStep, len(stepNames))
	for i, n := range stepNames {
		steps[i] = workflowStep{name: n, status: stepPending}
	}
	if maxRounds < 1 {
		maxRounds = 1
	}
	return &workflowBlock{
		name:      name,
		task:      task,
		round:     1,
		maxRounds: maxRounds,
		steps:     steps,
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
	case workflow.StepStarted:
		idx := b.stepIndex(e.Step)
		if idx < 0 {
			return
		}
		b.steps[idx].status = stepActive
		b.steps[idx].started = time.Now()
		b.steps[idx].message = ""
		b.active = idx
		if e.Round > 0 {
			b.round = e.Round
		}
		b.detail = ""
	case workflow.StepCompleted:
		idx := b.stepIndex(e.Step)
		if idx < 0 {
			return
		}
		b.steps[idx].status = stepCompleted
		b.steps[idx].finished = time.Now()
		if b.active == idx {
			b.active = -1
		}
	case workflow.StepFailed:
		idx := b.stepIndex(e.Step)
		if idx < 0 {
			return
		}
		b.steps[idx].status = stepFailed
		b.steps[idx].finished = time.Now()
		b.steps[idx].message = e.Message
		if b.active == idx {
			b.active = -1
		}
	case workflow.RoundRetrying:
		if e.Round > 0 {
			b.round = e.Round
		}
		// Every step from RetryFrom onward will re-execute on the next
		// round, so reset them all — not just the one that failed. Steps
		// before RetryFrom keep their state since the engine won't revisit
		// them. If the event lacks a step name we fall back to resetting
		// only rows marked stepFailed.
		resetFrom := len(b.steps) // sentinel: only reset failed
		if e.Step != "" {
			if idx := b.stepIndex(e.Step); idx >= 0 {
				resetFrom = idx
			}
		}
		for i := range b.steps {
			if i >= resetFrom || b.steps[i].status == stepFailed {
				b.steps[i] = workflowStep{name: b.steps[i].name, status: stepPending}
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

// ApplyAgent updates the active step's activity log + the cached detail
// string used by the status bar. EventToolCall prepends a "running" entry;
// EventToolResult marks the most recent unfinished entry as completed (with
// a duration) but does not clear the detail — that's the persistence trick
// that stops short-lived tool calls from flashing past too quickly to read.
func (b *workflowBlock) ApplyAgent(stepName string, ev agent.Event) {
	if b.active < 0 || b.steps[b.active].name != stepName {
		return
	}
	s := &b.steps[b.active]
	switch ev.Kind {
	case agent.EventToolCall:
		desc := toolVerb(ev.Name, ev.Args)
		s.activity = append([]activityEntry{{desc: desc, startedAt: time.Now()}}, s.activity...)
		if len(s.activity) > activityCap {
			s.activity = s.activity[:activityCap]
		}
		b.detail = desc
	case agent.EventToolResult:
		for i := range s.activity {
			if s.activity[i].finishedAt.IsZero() {
				s.activity[i].finishedAt = time.Now()
				break
			}
		}
		// Intentionally do NOT clear b.detail — keep the last action
		// visible in the status bar until the next tool call replaces it.
	}
}

func (b *workflowBlock) stepIndex(name string) int {
	for i, s := range b.steps {
		if s.name == name {
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
		sb.WriteString(styMuted.Render("  "+truncate(oneLine(b.task), limit)) + "\n")
	}
	sb.WriteString("\n")

	// Step rows. Pad names to a common column width for alignment.
	nameW := 0
	for _, s := range b.steps {
		if len(s.name) > nameW {
			nameW = len(s.name)
		}
	}
	total := len(b.steps)
	for i, s := range b.steps {
		// When an active step has an activity log we suppress the row's
		// detail column so the running tool isn't shown twice (once in
		// the row, once at the top of the log).
		rowDetail := b.detail
		if s.status == stepActive && len(s.activity) > 0 {
			rowDetail = ""
		}
		sb.WriteString("  " + renderStepRow(s, i+1, total, nameW, rowDetail))
		sb.WriteString("\n")
		if s.status == stepActive && len(s.activity) > 0 {
			for _, a := range s.activity {
				sb.WriteString("      " + renderActivityEntry(a) + "\n")
			}
		}
	}

	// Terminal summary.
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

func renderStepRow(s workflowStep, index, total, nameW int, activeDetail string) string {
	glyph, glyphCol := stepGlyph(s.status)
	glyphStr := lipgloss.NewStyle().Foreground(glyphCol).Render(glyph)

	name := padRight(s.name, nameW+2)
	switch s.status {
	case stepPending:
		name = styDim.Render(name)
	case stepActive:
		name = lipgloss.NewStyle().Foreground(glyphCol).Render(name)
	}

	counter := styDim.Render(fmt.Sprintf("%d/%d", index, total))

	detail := ""
	switch s.status {
	case stepCompleted:
		d := s.finished.Sub(s.started)
		if d > 0 {
			detail = "  " + styMuted.Render(fmtElapsed(d.Round(100*time.Millisecond)))
		}
	case stepFailed:
		if s.message != "" {
			detail = "  " + styErr.Render(truncate(oneLine(s.message), 60))
		}
	case stepActive:
		if activeDetail != "" {
			detail = "  " + styMuted.Render(truncate(oneLine(activeDetail), 60))
		}
	}

	return fmt.Sprintf("%s %s %s%s", glyphStr, name, counter, detail)
}

// renderActivityEntry formats one row of the per-step activity log. While
// the tool is in flight the entry uses ▶ + cyan; once finished it switches
// to ✓ + green plus an elapsed time suffix.
func renderActivityEntry(a activityEntry) string {
	var glyph string
	var glyphCol color.Color
	var dur string
	if a.finishedAt.IsZero() {
		glyph = "▶"
		glyphCol = colDotTool
	} else {
		glyph = "✓"
		glyphCol = colOK
		d := a.finishedAt.Sub(a.startedAt)
		if d > 0 {
			dur = "  " + styDim.Render(fmtElapsed(d.Round(10*time.Millisecond)))
		}
	}
	return lipgloss.NewStyle().Foreground(glyphCol).Render(glyph) + " " +
		styMuted.Render(a.desc) + dur
}

func stepGlyph(s stepStatus) (string, color.Color) {
	switch s {
	case stepActive:
		return "▶", colDotThinking
	case stepCompleted:
		return "✓", colOK
	case stepFailed:
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
