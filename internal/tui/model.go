package tui

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/agent"
)

// Run starts the Bubble Tea chat TUI. It returns when the user quits.
func Run(ctx context.Context, ag *agent.Agent, model, version string, wf WorkflowConfig) error {
	m, err := newModel(ctx, ag, model, version, wf)
	if err != nil {
		return err
	}
	// AltScreen + MouseMode are properties of the View in v2 (see View()).
	p := tea.NewProgram(m)
	m.send = p.Send
	// Pipe agent events directly into the Bubble Tea program. This avoids a
	// hand-rolled channel pump and the back-pressure that came with it.
	ag.SetEventHandler(func(e agent.Event) { p.Send(agentEventMsg{ev: e}) })
	_, err = p.Run()
	return err
}

type sendResultMsg struct{ err error }
type agentEventMsg struct{ ev agent.Event }

type model struct {
	ctx      context.Context
	ag       *agent.Agent
	modelTag string

	cwd    string
	branch string

	width, height int

	viewport viewport.Model
	input    textarea.Model
	spin     spinner.Model
	caption  string

	blocks []block
	md     *glamour.TermRenderer

	busy        bool
	busySince   time.Time
	currentTool *toolCallBlock
	quitting    bool

	// cancel for the currently in-flight Send, if any.
	sendCancel context.CancelFunc

	// mdStyleName is the glamour style chosen at startup. We re-use it when
	// recreating the renderer on resize so we never re-probe the terminal
	// from inside raw mode (which leaks the OSC 11 reply into the textarea).
	mdStyleName string

	// Workflow plumbing. Set after the Bubble Tea program is constructed in
	// Run(); workflow goroutines use send to push messages back.
	wf             WorkflowConfig
	send           teaSendFn
	activeWorkflow *workflowBlock
	workflowCancel context.CancelFunc
}

func newModel(ctx context.Context, ag *agent.Agent, modelTag, version string, wf WorkflowConfig) (*model, error) {
	// Detect dark/light once, here, before Bubble Tea puts stdin in raw mode.
	// Glamour's WithAutoStyle issues an OSC 11 query each time; doing that
	// from inside Update (e.g. on resize) leaks the terminal's reply into the
	// textarea. We capture the chosen style and reuse it.
	styleName := "light"
	if lipgloss.HasDarkBackground(os.Stdin, os.Stdout) {
		styleName = "dark"
	}
	md, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(styleName),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		md = nil
	}

	ta := textarea.New()
	ta.Placeholder = defaultPlaceholder
	ta.Prompt = "› "
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.Focus()

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.MouseWheelEnabled = true

	sp := spinner.New()
	sp.Spinner = statusSpinner
	sp.Style = lipgloss.NewStyle().Foreground(colDotThinking)

	cwd, _ := os.Getwd()
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, cwd); err == nil && !strings.HasPrefix(rel, "..") {
			cwd = "~/" + rel
		}
	}

	branch := gitBranch()
	if version == "" {
		version = "dev"
	}
	m := &model{
		ctx:         ctx,
		ag:          ag,
		modelTag:    modelTag,
		mdStyleName: styleName,
		cwd:         cwd,
		branch:      branch,
		viewport:    vp,
		input:       ta,
		spin:        sp,
		caption:     randomCaption(),
		md:          md,
		wf:          wf,
	}
	// Welcome banner shown once at the top of scrollback.
	m.blocks = append(m.blocks, splashBlock{
		version: version,
		model:   modelTag,
		cwd:     cwd,
		branch:  branch,
	})
	return m, nil
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(
		m.spin.Tick,
		rotateCaptionEvery(3*time.Second),
	)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		m.refreshViewport()

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+d":
			if m.sendCancel != nil {
				m.sendCancel()
			}
			if m.workflowCancel != nil {
				m.workflowCancel()
			}
			m.quitting = true
			return m, tea.Quit
		case "esc":
			// Soft interrupt: cancel whatever is in flight without quitting.
			if m.busy && m.sendCancel != nil {
				m.sendCancel()
			}
			if m.activeWorkflow != nil && m.workflowCancel != nil {
				m.workflowCancel()
			}
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				break
			}
			// Slash commands are parsed before the busy / active-workflow
			// gate so /cancel remains reachable while a workflow is running.
			if strings.HasPrefix(text, "/") {
				m.input.Reset()
				m.resizeInput()
				m.handleSlashCommand(text)
				break
			}
			// Chat text is suppressed while a turn or workflow is in flight.
			if m.busy || m.activeWorkflow != nil {
				break
			}
			m.input.Reset()
			m.resizeInput()
			m.appendBlock(userBlock{text: text})
			m.busy = true
			m.busySince = time.Now()
			m.caption = randomCaption()
			m.setDotColor(colDotThinking)
			cmds = append(cmds, m.startSend(text))
		case "shift+enter", "alt+enter", "ctrl+j":
			// Insert a newline. Most terminals don't distinguish shift+enter
			// from enter without enhanced-key reporting; alt+enter and
			// ctrl+j are the portable fallbacks.
			m.input.InsertString("\n")
			m.resizeInput()
		case "ctrl+l":
			m.blocks = nil
			m.refreshViewport()
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			m.resizeInput()
		}

	case agentEventMsg:
		m.handleEvent(msg.ev)
		// Tie the dot's color to whether a tool is currently running.
		if m.currentTool != nil {
			m.setDotColor(colDotTool)
		} else if m.busy {
			m.setDotColor(colDotThinking)
		}

	case workflowEventMsg:
		if m.activeWorkflow != nil {
			m.activeWorkflow.Apply(msg.ev)
			m.refreshViewport()
		}

	case workflowAgentEventMsg:
		if m.activeWorkflow != nil {
			m.activeWorkflow.ApplyAgent(msg.step, msg.ev)
			m.refreshViewport()
		}

	case workflowDoneMsg:
		if m.workflowCancel != nil {
			m.workflowCancel()
			m.workflowCancel = nil
		}
		m.activeWorkflow = nil
		m.input.Placeholder = defaultPlaceholder
		if msg.err != nil && msg.err != context.Canceled {
			m.appendBlock(errorBlock{err: msg.err})
		}

	case sendResultMsg:
		m.busy = false
		m.currentTool = nil
		if m.sendCancel != nil {
			m.sendCancel()
			m.sendCancel = nil
		}
		if msg.err != nil && msg.err != context.Canceled {
			m.appendBlock(errorBlock{err: msg.err})
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		cmds = append(cmds, cmd)

	case rotateCaptionMsg:
		if m.busy {
			m.caption = randomCaption()
		}
		cmds = append(cmds, rotateCaptionEvery(3*time.Second))

	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	if m.width == 0 {
		return makeView("loading…")
	}

	status := m.statusLine()
	footer := m.footerLine()
	inputBar := styInputBar.Width(m.width).Render(m.input.View())

	content := lipgloss.JoinVertical(lipgloss.Left,
		m.viewport.View(),
		status,
		inputBar,
		footer,
	)
	return makeView(content)
}

// makeView wraps a rendered string with the v2 View settings we want for
// every frame: alt screen + cell-motion mouse. KeyboardEnhancements defaults
// to "basic key disambiguation", which gives shift+enter on terminals that
// support the Kitty keyboard protocol (Kitty, Ghostty, WezTerm, recent iTerm2).
func makeView(content string) tea.View {
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// elapsedThreshold is how long a turn has to run before we surface an
// elapsed-time counter or fall back to the playful caption. Short turns
// shouldn't flicker numbers or trivia at the user.
const elapsedThreshold = 3 * time.Second

const (
	defaultPlaceholder  = "Ask neo anything…   ⌥↩ newline · ↩ send"
	workflowPlaceholder = "workflow running — Esc to cancel"
)

// handleSlashCommand parses slash commands. Called only when input begins
// with '/'.
func (m *model) handleSlashCommand(line string) {
	parts := strings.Fields(line)
	cmd := parts[0]
	switch cmd {
	case "/run":
		if len(parts) < 2 {
			m.appendBlock(errorBlock{err: fmt.Errorf("usage: /run <flow-name> [task]")})
			return
		}
		name := parts[1]
		task := strings.TrimSpace(strings.Join(parts[2:], " "))
		m.startWorkflowCmd(name, task)
	case "/cancel":
		if m.activeWorkflow == nil {
			m.appendBlock(errorBlock{err: fmt.Errorf("no workflow running")})
			return
		}
		if m.workflowCancel != nil {
			m.workflowCancel()
		}
	case "/flows":
		m.appendBlock(buildFlowsBlock(m.wf.Config))
	case "/help":
		m.appendBlock(helpBlock{})
	default:
		m.appendBlock(errorBlock{err: fmt.Errorf("unknown command: %s — try /help", cmd)})
	}
}

// startWorkflowCmd loads the named flow definition and kicks off an engine
// run. Adds a workflowBlock to the scrollback to render its progress.
//
// Rejects the request if a workflow is already active; without this guard,
// the previous run's goroutine keeps emitting workflowEventMsg /
// workflowDoneMsg and Update would apply them to the new block, mixing
// progress between runs (and clearing the new run early when the old one
// finishes).
func (m *model) startWorkflowCmd(name, task string) {
	if m.activeWorkflow != nil {
		m.appendBlock(errorBlock{err: fmt.Errorf("a workflow is already running — /cancel it first")})
		return
	}
	// Also reject when a chat turn is in flight. The chat agent and the
	// workflow's phase.Runner share the same Provider/Tools instance; the
	// engine would also try to take over Runner.OnEvent, which is being
	// driven by the chat turn and would race with it.
	if m.busy {
		m.appendBlock(errorBlock{err: fmt.Errorf("a chat turn is in flight — wait or Esc to cancel before /run")})
		return
	}
	def, err := m.wf.definitionFor(name)
	if err != nil {
		m.appendBlock(errorBlock{err: err})
		return
	}
	block := newWorkflowBlock(def.Name, task, def.Steps, def.MaxRounds)
	m.activeWorkflow = block
	m.appendBlock(block)
	m.input.Placeholder = workflowPlaceholder
	m.setDotColor(colDotWorkflow)
	m.workflowCancel = m.wf.launchWorkflow(m.ctx, m.send, def, task)
}

// setDotColor swaps the spinner's foreground so the pulsing dot reflects
// the current state (thinking vs. tool-active). Called from Update on the
// transitions that actually change state.
func (m *model) setDotColor(c color.Color) {
	m.spin.Style = lipgloss.NewStyle().Foreground(c)
}

func (m *model) statusLine() string {
	// Workflow has priority — its dot color and label override the chat state.
	if m.activeWorkflow != nil {
		return " " + m.spin.View() + " " + styMuted.Render(m.workflowStatusBody())
	}

	if !m.busy {
		// Steady green dot when idle — no pulse, no spinner machinery.
		dot := lipgloss.NewStyle().Foreground(colDotReady).Render("●")
		return " " + dot + " " + styMuted.Render("ready")
	}

	elapsed := time.Since(m.busySince)

	// Pick the body text based on what the agent is actually doing.
	var body string
	switch {
	case m.currentTool != nil:
		body = toolVerb(m.currentTool.name, m.currentTool.args)
	case elapsed >= elapsedThreshold:
		// Long think with no active tool — drop in a playful caption.
		body = m.caption
	default:
		body = "thinking"
	}

	line := " " + m.spin.View() + " " + styMuted.Render(body)
	if elapsed >= elapsedThreshold {
		line += "  " + styDim.Render(formatElapsed(elapsed))
	}
	return line
}

// workflowStatusBody describes the current workflow step for the status line.
func (m *model) workflowStatusBody() string {
	w := m.activeWorkflow
	if w.active >= 0 && w.active < len(w.steps) {
		step := w.steps[w.active].name
		s := fmt.Sprintf("workflow: %s · %d/%d", step, w.active+1, len(w.steps))
		if w.detail != "" {
			s += " · " + w.detail
		}
		return s
	}
	return fmt.Sprintf("workflow: %s · round %d/%d", w.name, w.round, w.maxRounds)
}

func formatElapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d / time.Minute)
	s := int(d/time.Second) % 60
	return fmt.Sprintf("%dm%02ds", m, s)
}

func (m *model) footerLine() string {
	left := fmt.Sprintf("%s (%s)", m.cwd, m.branch)
	right := m.modelTag
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return styFooter.Render(left + strings.Repeat(" ", gap) + right)
}

// inputMaxRows caps how tall the input grows; beyond that the textarea
// scrolls internally.
const inputMaxRows = 8

// resizeInput recomputes the textarea height from its current contents and
// re-runs layout if the height changed.
func (m *model) resizeInput() {
	want := m.input.LineCount()
	if want < 1 {
		want = 1
	}
	if want > inputMaxRows {
		want = inputMaxRows
	}
	if want != m.input.Height() {
		m.input.SetHeight(want)
		m.layout()
	}
}

func (m *model) layout() {
	inputHeight := m.input.Height() + 2 // textarea body + top/bottom border
	chrome := inputHeight + 2           // status + footer lines
	vpH := m.height - chrome
	if vpH < 3 {
		vpH = 3
	}
	m.viewport.SetWidth(m.width)
	m.viewport.SetHeight(vpH)
	m.input.SetWidth(m.width - 2)
	if m.md != nil {
		// Re-create renderer at the new width so code blocks wrap nicely.
		// Use the cached style name — no re-probing the terminal here.
		if r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle(m.mdStyleName),
			glamour.WithWordWrap(m.width-2),
		); err == nil {
			m.md = r
		}
	}
}

func (m *model) appendBlock(b block) {
	m.blocks = append(m.blocks, b)
	m.refreshViewport()
}

func (m *model) refreshViewport() {
	if m.width == 0 {
		return
	}
	var sb strings.Builder
	for i, b := range m.blocks {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(b.render(m.width, m.md))
		sb.WriteString("\n")
	}
	m.viewport.SetContent(sb.String())
	m.viewport.GotoBottom()
}

func (m *model) handleEvent(e agent.Event) {
	switch e.Kind {
	case agent.EventAssistantText:
		if strings.TrimSpace(e.Text) != "" {
			m.appendBlock(textBlock{text: e.Text})
		}
	case agent.EventToolCall:
		tc := toolCallBlock{name: e.Name, args: e.Args, startAt: time.Now()}
		m.currentTool = &tc
		m.appendBlock(tc)
	case agent.EventToolResult:
		elapsed := time.Duration(0)
		if m.currentTool != nil {
			elapsed = time.Since(m.currentTool.startAt)
		}
		m.appendBlock(toolResultBlock{
			name:    e.Name,
			text:    e.Text,
			isError: e.IsError,
			elapsed: elapsed,
		})
		m.currentTool = nil
	case agent.EventError:
		m.appendBlock(errorBlock{err: e.Err})
	case agent.EventDone:
		// handled when sendResultMsg arrives
	}
}

func (m *model) startSend(text string) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.sendCancel = cancel
	return func() tea.Msg {
		_, err := m.ag.Send(ctx, text)
		return sendResultMsg{err: err}
	}
}

func gitBranch() string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "no-git"
	}
	return strings.TrimSpace(string(out))
}
