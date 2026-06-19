package tui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/factory"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/projectctx"
	"github.com/owainlewis/neo/internal/session"
	"github.com/owainlewis/neo/internal/skills"
	"github.com/owainlewis/neo/internal/workflow"
	"github.com/owainlewis/neo/internal/workspace"
)

type Options struct {
	AfterSend       func() error
	PermissionMode  string
	SessionStore    *session.Store
	CurrentSession  *session.Session
	OnSessionResume func(*session.Session)
	ModelChoices    []ModelChoice
	ProjectRoot     string
	MemoryEnabled   bool
	StepEvents      <-chan factory.Event
	WorkflowEvents  <-chan workflow.Event
}

type Option func(*Options)

func WithAfterSend(fn func() error) Option {
	return func(opts *Options) { opts.AfterSend = fn }
}

func WithPermissionMode(mode string) Option {
	return func(opts *Options) { opts.PermissionMode = mode }
}

func WithSessions(store *session.Store, current *session.Session, onResume func(*session.Session)) Option {
	return func(opts *Options) {
		opts.SessionStore = store
		opts.CurrentSession = current
		opts.OnSessionResume = onResume
	}
}

func WithModelChoices(choices []ModelChoice) Option {
	return func(opts *Options) { opts.ModelChoices = choices }
}

func WithProjectMemory(root string, enabled bool) Option {
	return func(opts *Options) {
		opts.ProjectRoot = root
		opts.MemoryEnabled = enabled
	}
}

// WithStepEvents subscribes the TUI to the factory supervisor's event
// stream, which the chat view folds into live subagent trees while agent
// calls execute.
func WithStepEvents(ch <-chan factory.Event) Option {
	return func(opts *Options) { opts.StepEvents = ch }
}

func WithWorkflowEvents(ch <-chan workflow.Event) Option {
	return func(opts *Options) { opts.WorkflowEvents = ch }
}

// Run starts the Bubble Tea chat TUI. It returns when the user quits. sk is the
// loaded skill set used for $name expansion (nil when the feature is off).
func Run(ctx context.Context, ag *agent.Agent, model, version string, sk []skills.Skill, options ...Option) error {
	opts := Options{}
	for _, option := range options {
		option(&opts)
	}
	m, err := newModel(ctx, ag, model, version, sk, opts)
	if err != nil {
		return err
	}
	// AltScreen + MouseMode are properties of the View in v2 (see View()).
	p := tea.NewProgram(m)
	// Pipe agent events directly into the Bubble Tea program. This avoids a
	// hand-rolled channel pump and the back-pressure that came with it.
	ag.SetEventHandler(func(e agent.Event) { p.Send(agentEventMsg{ev: e}) })
	// Supervisor events (subagent activity during agent calls) arrive the same
	// way. The channel is already non-blocking on the producer side.
	if opts.StepEvents != nil {
		go func() {
			for ev := range opts.StepEvents {
				p.Send(stepEventMsg{ev: ev})
			}
		}()
	}
	if opts.WorkflowEvents != nil {
		go func() {
			for ev := range opts.WorkflowEvents {
				p.Send(workflowEventMsg{ev: ev})
			}
		}()
	}
	ag.SetApprover(func(ctx context.Context, req agent.ApprovalRequest) (bool, error) {
		reply := make(chan bool, 1)
		p.Send(approvalRequestMsg{req: req, reply: reply})
		select {
		case ok := <-reply:
			return ok, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	})
	_, err = p.Run()
	return err
}

type sendResultMsg struct{ err error }
type agentEventMsg struct{ ev agent.Event }
type stepEventMsg struct{ ev factory.Event }
type workflowEventMsg struct{ ev workflow.Event }
type approvalRequestMsg struct {
	req   agent.ApprovalRequest
	reply chan bool
}

type approvalState struct {
	req   agent.ApprovalRequest
	reply chan bool
}

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
	picker   commandPicker
	files    filePicker
	sessions sessionBrowser
	models   modelBrowser
	perms    permissionPicker

	// lastInputHeight is the textarea height the current layout was computed
	// for. When the textarea grows/shrinks (DynamicHeight), this lets us
	// detect the change and re-layout the viewport around it.
	lastInputHeight int

	blocks []block
	md     *glamour.TermRenderer

	busy                bool
	busySince           time.Time
	currentTool         *toolCallBlock
	workflow            *workflowBlock
	autoWorkflowPending bool
	activeTree          *treeBlock         // block receiving new top-level subagent trees
	treeIndex           map[int]*treeBlock // supervisor node id -> the block holding it
	approval            *approvalState
	// allow holds the rules the user granted via "always allow" during this
	// session. It is consulted before prompting, so a granted tool/command
	// stops asking. It is intentionally not persisted.
	allow    permission.Allowlist
	quitting bool

	// cancel for the currently in-flight Send, if any.
	sendCancel context.CancelFunc

	// mdStyleName is the glamour style chosen at startup. We re-use it when
	// recreating the renderer on resize so we never re-probe the terminal
	// from inside raw mode (which leaks the OSC 11 reply into the textarea).
	mdStyleName string

	// skills drives $name expansion of the user's input before it's sent.
	skills []skills.Skill

	afterSend         func() error
	permissionMode    string
	sessionStore      *session.Store
	currentSessionID  string
	currentSessionCWD string
	onSessionResume   func(*session.Session)
	modelChoices      []ModelChoice
	projectRoot       string
	memoryEnabled     bool
}

func newModel(ctx context.Context, ag *agent.Agent, modelTag, version string, sk []skills.Skill, opts Options) (*model, error) {
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
	// Let the textarea grow and shrink to fit its content. DynamicHeight
	// accounts for soft-wrapped lines (a long unwrapped paragraph still
	// expands), which a manual LineCount() height never could. The box stays
	// between one row and inputMaxRows; past that it scrolls internally.
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = inputMaxRows
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	// Give the textarea the same solid background as its wrapping bar so the
	// composer reads as one continuous block (Codex-style), with no seams
	// between the textarea cells and the surrounding padding.
	taStyles := ta.Styles()
	for _, st := range []*textarea.StyleState{&taStyles.Focused, &taStyles.Blurred} {
		st.Base = st.Base.Background(colInputBg)
		st.Text = st.Text.Background(colInputBg)
		st.Prompt = st.Prompt.Background(colInputBg)
		st.Placeholder = st.Placeholder.Background(colInputBg)
		st.CursorLine = st.CursorLine.Background(colInputBg)
		st.EndOfBuffer = st.EndOfBuffer.Background(colInputBg)
	}
	ta.SetStyles(taStyles)
	ta.Focus()

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.MouseWheelEnabled = true
	// Content is word-wrapped to the viewport width, so there is nothing to
	// scroll to horizontally. A horizontal trackpad swipe emits a wheel-right
	// (or shift+wheel) event that would otherwise slide the whole view
	// sideways — which reads as a bug. Zeroing the horizontal step disables
	// that motion while leaving vertical wheel scrolling intact.
	vp.SetHorizontalStep(0)

	sp := spinner.New()
	sp.Spinner = statusSpinner
	sp.Style = lipgloss.NewStyle().Foreground(colDotThinking)

	absCWD, _ := os.Getwd()
	cwd := absCWD
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, cwd); err == nil && !strings.HasPrefix(rel, "..") {
			cwd = "~/" + rel
		}
	}

	branch := gitBranch()
	if version == "" {
		version = "dev"
	}
	currentSessionID := ""
	currentSessionCWD := absCWD
	if opts.CurrentSession != nil {
		currentSessionID = opts.CurrentSession.Metadata.ID
		if opts.CurrentSession.Metadata.CWD != "" {
			currentSessionCWD = opts.CurrentSession.Metadata.CWD
		}
	}

	m := &model{
		ctx:               ctx,
		ag:                ag,
		modelTag:          modelTag,
		mdStyleName:       styleName,
		cwd:               cwd,
		branch:            branch,
		viewport:          vp,
		input:             ta,
		spin:              sp,
		caption:           randomCaption(),
		files:             newFilePicker(workspace.Root(absCWD)),
		md:                md,
		skills:            sk,
		afterSend:         opts.AfterSend,
		permissionMode:    opts.PermissionMode,
		sessionStore:      opts.SessionStore,
		currentSessionID:  currentSessionID,
		currentSessionCWD: currentSessionCWD,
		onSessionResume:   opts.OnSessionResume,
		modelChoices:      normalizeModelChoices(modelTag, opts.ModelChoices),
		projectRoot:       opts.ProjectRoot,
		memoryEnabled:     opts.MemoryEnabled,
	}
	// Welcome banner shown once at the top of scrollback.
	m.blocks = append(m.blocks, splashBlock{
		version: version,
		model:   modelTag,
		cwd:     cwd,
		branch:  branch,
		tagline: randomTagline(),
	})
	m.appendTranscript(ag.Transcript())
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
		if m.perms.visible {
			return m.handlePermissionPickerKey(msg)
		}
		if m.models.visible {
			return m.handleModelBrowserKey(msg)
		}
		if m.sessions.visible {
			return m.handleSessionBrowserKey(msg)
		}
		if m.approval != nil {
			switch msg.String() {
			case "ctrl+c", "ctrl+d":
				m.finishApproval(false)
				if m.sendCancel != nil {
					m.sendCancel()
				}
				m.quitting = true
				return m, tea.Quit
			case "y", "Y":
				m.finishApproval(true)
			case "a", "A":
				req := permission.Request{ToolName: m.approval.req.ToolName, Args: m.approval.req.Args}
				rule := permission.RuleFor(req)
				m.allow.Add(rule)
				m.finishApproval(true)
				m.appendBlock(noticeBlock{text: "won't ask again for " + rule.Label() + " this session"})
			case "n", "N", "esc":
				m.finishApproval(false)
			}
			break
		}
		switch msg.String() {
		case "ctrl+c", "ctrl+d":
			if m.sendCancel != nil {
				m.sendCancel()
			}
			m.quitting = true
			return m, tea.Quit
		case "esc":
			if m.files.visible {
				m.dismissFilePicker()
				m.layout()
				break
			}
			if m.picker.visible {
				m.dismissSlashPicker()
				m.layout()
				break
			}
			// Soft interrupt: cancel the in-flight turn without quitting.
			if m.busy && m.sendCancel != nil {
				m.sendCancel()
			}
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				break
			}
			if m.picker.visible && m.acceptSlashPicker(false) {
				m.syncInputHeight()
				m.layout()
				break
			}
			if m.files.visible && m.acceptFilePicker() {
				m.syncInputHeight()
				m.layout()
				break
			}
			rawInput := text
			// Slash commands are parsed before the busy gate.
			if strings.HasPrefix(text, "/") {
				m.input.Reset()
				m.hideSlashPicker()
				m.hideFilePicker()
				m.layout()
				m.syncInputHeight()
				m.handleSlashCommand(text)
				break
			}
			// A leading ! is a direct shell command alias. It runs through the
			// agent's normal bash tool policy and rendering events, not as chat.
			if strings.HasPrefix(text, "!") {
				m.input.Reset()
				m.hideSlashPicker()
				m.hideFilePicker()
				m.layout()
				m.syncInputHeight()
				cmds = append(cmds, m.handleBangCommand(text))
				break
			}
			// Chat text is suppressed while a turn is in flight.
			if m.busy {
				break
			}
			m.input.Reset()
			m.hideSlashPicker()
			m.hideFilePicker()
			m.layout()
			m.syncInputHeight()
			// Pull any dragged/pasted image paths out of the input; they become
			// attachments on the message, the rest stays as text.
			text, images := extractImagePaths(text)
			m.appendBlock(userBlock{text: rawInput})
			m.maybeStartWorkflowFromUserText(text)
			if len(images) > 0 {
				m.appendBlock(noticeBlock{text: "attached image: " + strings.Join(shortPaths(images), ", ")})
			}
			// Expand any $name skill references: the user sees what they typed,
			// the agent receives the expanded message.
			sent, used := skills.Expand(text, m.skills)
			if len(used) > 0 {
				m.appendBlock(noticeBlock{text: "applied skill: " + strings.Join(used, ", ")})
			}
			m.busy = true
			m.busySince = time.Now()
			m.caption = randomCaption()
			m.setDotColor(colDotThinking)
			cmds = append(cmds, m.startSend(sent, images))
		case "shift+enter", "alt+enter", "ctrl+j":
			// Insert a newline. Most terminals don't distinguish shift+enter
			// from enter without enhanced-key reporting; alt+enter and
			// ctrl+j are the portable fallbacks.
			m.input.InsertString("\n")
			m.updateInlinePickers()
			m.syncInputHeight()
		case "up":
			if m.files.visible {
				m.moveFilePickerSelection(-1)
				break
			}
			if m.picker.visible {
				m.moveSlashPickerSelection(-1)
				break
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			m.updateInlinePickers()
			m.syncInputHeight()
		case "down":
			if m.files.visible {
				m.moveFilePickerSelection(1)
				break
			}
			if m.picker.visible {
				m.moveSlashPickerSelection(1)
				break
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			m.updateInlinePickers()
			m.syncInputHeight()
		case "tab":
			if m.files.visible && m.acceptFilePicker() {
				m.syncInputHeight()
				m.layout()
				break
			}
			if m.picker.visible && m.acceptSlashPicker(true) {
				m.syncInputHeight()
				m.layout()
				break
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			m.updateInlinePickers()
			m.syncInputHeight()
		case "ctrl+l":
			m.blocks = nil
			m.refreshViewport()
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			m.updateInlinePickers()
			m.syncInputHeight()
		}

	case tea.PasteMsg:
		if m.perms.visible || m.models.visible || m.sessions.visible || m.approval != nil {
			break
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
		m.updateInlinePickers()
		m.syncInputHeight()

	case agentEventMsg:
		m.handleEvent(msg.ev)
		// Tie the dot's color to whether a tool is currently running.
		if m.currentTool != nil {
			m.setDotColor(colDotTool)
		} else if m.busy {
			m.setDotColor(colDotThinking)
		}

	case approvalRequestMsg:
		req := permission.Request{ToolName: msg.req.ToolName, Args: msg.req.Args}
		if m.allow.Allows(req) {
			msg.reply <- true
			break
		}
		m.approval = &approvalState{req: msg.req, reply: msg.reply}
		m.appendBlock(approvalBlock{req: msg.req})

	case sendResultMsg:
		m.busy = false
		m.currentTool = nil
		if m.sendCancel != nil {
			m.sendCancel()
			m.sendCancel = nil
		}
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) && !errors.Is(msg.err, agent.ErrMaxTurns) {
			m.appendBlock(errorBlock{err: msg.err})
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		cmds = append(cmds, cmd)
		// A live tree shows elapsed time on its running nodes; repaint on
		// the spinner's cadence so the counters don't freeze between events.
		if m.activeTree != nil && m.activeTree.running() {
			m.refreshViewport()
		}

	case stepEventMsg:
		m.handleStepEvent(msg.ev)

	case workflowEventMsg:
		m.handleWorkflowEvent(msg.ev)

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
	if m.sessions.visible {
		return makeView(m.sessionBrowserView())
	}
	if m.models.visible {
		return makeView(m.modelBrowserView())
	}
	if m.perms.visible {
		return makeView(m.permissionPickerView())
	}

	status := m.statusLine()
	footer := m.footerLine()
	picker := m.inlinePickerView()
	// While an approval is pending the input is inert (all keys route to the
	// y/a/n handler), so the composer is replaced by a focused action bar.
	bottom := styInputBar.Width(m.width).Render(m.input.View())
	if m.approval != nil {
		bottom = m.approvalBarView()
	}

	parts := []string{m.viewport.View()}
	if workflow := m.workflowPanelView(); workflow != "" {
		parts = append(parts, workflow, "")
	}
	parts = append(parts,
		status,
		"",
		bottom,
	)
	if picker != "" {
		parts = append(parts, picker)
	}
	parts = append(parts, "", footer)

	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return makeView(content)
}

// makeView wraps a rendered string with the v2 View settings we want for
// every frame: alt screen + cell-motion mouse, plus a request for keyboard
// enhancements. ReportAlternateKeys asks terminals that speak the Kitty
// keyboard protocol (Kitty, Ghostty, WezTerm, recent iTerm2) to disambiguate
// shift+enter from a bare enter, which is what lets shift+enter insert a
// newline there. On terminals without it, alt+enter / ctrl+j remain the
// portable fallbacks.
func makeView(content string) tea.View {
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.KeyboardEnhancements.ReportAlternateKeys = true
	return v
}

// elapsedThreshold is how long a turn has to run before we surface an
// elapsed-time counter or fall back to the playful caption. Short turns
// shouldn't flicker numbers or trivia at the user.
const elapsedThreshold = 3 * time.Second

const defaultPlaceholder = "Ask neo anything…   ↩ send"

// handleSlashCommand parses slash commands. Called only when input begins
// with '/'.
func (m *model) handleSlashCommand(line string) {
	parts := strings.Fields(line)
	cmd := parts[0]
	if m.busy && slashCommandRequiresIdle(cmd) {
		m.appendBlock(errorBlock{err: fmt.Errorf("%s is unavailable while a turn is running", cmd)})
		return
	}
	switch cmd {
	case "/help":
		m.appendBlock(helpBlock{commands: m.slashCommands()})
	case "/tools":
		m.appendBlock(toolsBlock{specs: m.ag.ToolSpecs()})
	case "/permissions":
		m.openPermissionPicker()
	case "/tokens":
		m.appendBlock(tokensBlock{usage: m.ag.Usage()})
	case "/model":
		m.openModelBrowser()
	case "/memory":
		m.appendProjectMemory(strings.TrimSpace(strings.TrimPrefix(line, cmd)))
	case "/sessions":
		m.openSessionBrowser()
	case "/clear":
		m.ag.Clear()
		m.blocks = nil
		m.refreshViewport()
		if m.afterSend != nil {
			if err := m.afterSend(); err != nil {
				m.appendBlock(errorBlock{err: err})
			}
		}
	default:
		m.appendBlock(errorBlock{err: fmt.Errorf("unknown command: %s — try /help", cmd)})
	}
}

func slashCommandRequiresIdle(cmd string) bool {
	switch cmd {
	case "/clear", "/tokens", "/sessions", "/model", "/permissions", "/memory":
		return true
	default:
		return false
	}
}

func (m *model) appendProjectMemory(text string) {
	if !m.memoryEnabled {
		m.appendBlock(errorBlock{err: fmt.Errorf("unknown command: /memory — try /help")})
		return
	}
	if m.permissionMode == "readonly" {
		m.appendBlock(errorBlock{err: fmt.Errorf("/memory is unavailable in readonly mode because it writes project files")})
		return
	}
	if m.projectRoot == "" {
		m.appendBlock(errorBlock{err: fmt.Errorf("/memory is unavailable because the project root could not be determined")})
		return
	}
	path, err := projectctx.AppendMemory(m.projectRoot, text, time.Now())
	if err != nil {
		m.appendBlock(errorBlock{err: err})
		return
	}
	m.appendBlock(noticeBlock{text: "saved project memory to " + path})
}

func (m *model) updateInlinePickers() {
	m.updateSlashPicker()
	if m.picker.visible {
		m.hideFilePicker()
		return
	}
	m.updateFilePicker()
}

func (m *model) inlinePickerView() string {
	if out := m.slashPickerView(); out != "" {
		return out
	}
	return m.filePickerView()
}

// handleBangCommand parses !shell aliases. Called only when input begins with
// '!'. The command is intentionally not appended as a user chat message.
func (m *model) handleBangCommand(line string) tea.Cmd {
	command := strings.TrimSpace(strings.TrimPrefix(line, "!"))
	if command == "" {
		m.appendBlock(errorBlock{err: fmt.Errorf("type a shell command after !, for example !git status")})
		return nil
	}
	if m.busy {
		m.appendBlock(errorBlock{err: fmt.Errorf("! is unavailable while a turn is running")})
		return nil
	}
	m.busy = true
	m.busySince = time.Now()
	m.caption = randomCaption()
	m.setDotColor(colDotThinking)
	return m.startTool("bash", map[string]any{"command": command})
}

func (m *model) finishApproval(ok bool) {
	if m.approval == nil {
		return
	}
	m.approval.reply <- ok
	if ok {
		m.appendBlock(noticeBlock{text: "approved " + m.approval.req.ToolName})
	} else {
		m.appendBlock(noticeBlock{text: "denied " + m.approval.req.ToolName})
	}
	m.approval = nil
}

// setDotColor swaps the spinner's foreground so the pulsing dot reflects
// the current state (thinking vs. tool-active). Called from Update on the
// transitions that actually change state.
func (m *model) setDotColor(c color.Color) {
	m.spin.Style = lipgloss.NewStyle().Foreground(c)
}

func (m *model) statusLine() string {
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

// syncInputHeight re-runs layout when the textarea's (self-managed, soft-wrap
// aware) height has changed since the last frame, so the viewport resizes to
// make room. The textarea recalculates its own height on every edit because
// DynamicHeight is enabled; we only react to the result.
func (m *model) syncInputHeight() {
	if m.input.Height() != m.lastInputHeight {
		m.lastInputHeight = m.input.Height()
		m.layout()
	}
}

func (m *model) layout() {
	inputHeight := m.input.Height() + 2 // textarea body + top/bottom padding
	pickerHeight := 0
	if m.picker.visible && len(m.picker.matches) > 0 {
		pickerHeight = len(m.picker.matches) + 1
	} else if m.files.visible && len(m.files.matches) > 0 {
		pickerHeight = len(m.files.matches) + 1
	}
	workflowHeight := m.workflowPanelHeight()
	chrome := inputHeight + pickerHeight + workflowHeight + 4 // status + footer lines + margin above/below input
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

func (m *model) workflowPanelView() string {
	if m.workflow == nil {
		return ""
	}
	return m.workflow.render(m.width, nil)
}

func (m *model) workflowPanelHeight() int {
	panel := m.workflowPanelView()
	if panel == "" {
		return 0
	}
	return strings.Count(panel, "\n") + 2 // panel lines plus one-line margin before status
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
		m.activeTree = nil // assistant commentary splits subagent trees
		if strings.TrimSpace(e.Text) != "" {
			m.appendBlock(textBlock{text: e.Text})
		}
	case agent.EventToolCall:
		if e.Name == "workflow" {
			// The workflow tool mutates the checklist through workflowEventMsg;
			// don't show a duplicate generic tool card.
			break
		}
		m.noteWorkflowActivity(toolActivity(e.Name, e.Args))
		tc := toolCallBlock{name: e.Name, args: e.Args, startAt: time.Now()}
		m.currentTool = &tc
		if e.Name == "agent" {
			// The supervisor's "start" event draws this call as a tree
			// node; no generic tool card.
			break
		}
		m.activeTree = nil
		m.appendBlock(tc)
	case agent.EventToolResult:
		if e.Name == "workflow" {
			break
		}
		elapsed := time.Duration(0)
		if m.currentTool != nil {
			elapsed = time.Since(m.currentTool.startAt)
		}
		m.currentTool = nil
		if e.Name == "agent" {
			// Success renders in the tree. Failures/denials keep an error card
			// so the output is inspectable.
			if e.IsError || !runStepOK(e.Text) {
				m.appendBlock(toolResultBlock{name: e.Name, text: e.Text, isError: true, elapsed: elapsed})
			}
			break
		}
		m.appendBlock(toolResultBlock{
			name:    e.Name,
			text:    e.Text,
			isError: e.IsError,
			elapsed: elapsed,
		})
	case agent.EventError:
		m.appendBlock(errorBlock{err: e.Err})
	case agent.EventMaxTurnsReached:
		m.appendBlock(maxTurnsBlock{limit: e.MaxTurns})
	case agent.EventDone:
		// handled when sendResultMsg arrives
	}
}

var (
	numberedWorkflowLine = regexp.MustCompile(`^\s*(?:[-*]\s*)?(\d+)[.)]\s+(.+?)\s*$`)
	bulletWorkflowLine   = regexp.MustCompile(`^\s*[-*]\s+(.+?)\s*$`)
)

func (m *model) maybeStartWorkflowFromUserText(text string) {
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "workflow") {
		return
	}
	var items []workflow.Item
	for _, line := range strings.Split(text, "\n") {
		match := numberedWorkflowLine.FindStringSubmatch(line)
		if len(match) == 3 {
			itemText := strings.TrimSpace(match[2])
			if itemText != "" {
				items = append(items, workflow.Item{ID: match[1], Text: itemText, Status: workflow.Pending})
			}
			continue
		}
		match = bulletWorkflowLine.FindStringSubmatch(line)
		if len(match) == 2 {
			itemText := strings.TrimSpace(match[1])
			if itemText != "" {
				items = append(items, workflow.Item{ID: fmt.Sprint(len(items) + 1), Text: itemText, Status: workflow.Pending})
			}
		}
	}
	if len(items) < 2 {
		return
	}
	m.workflow = &workflowBlock{title: "Workflow", items: items}
	m.autoWorkflowPending = true
	m.layout()
	m.refreshViewport()
}

func (m *model) handleWorkflowEvent(ev workflow.Event) {
	if ev.Action == "clear" {
		m.workflow = nil
		m.layout()
		m.refreshViewport()
		return
	}
	if ev.Action == "create" {
		if m.autoWorkflowPending && m.workflow != nil {
			m.workflow.title = ev.State.Title
			m.workflow.items = ev.State.Items
			m.workflow.active = ""
			m.autoWorkflowPending = false
			m.layout()
			m.refreshViewport()
			return
		}
		m.autoWorkflowPending = false
		wb := &workflowBlock{title: ev.State.Title, items: ev.State.Items}
		m.workflow = wb
		m.layout()
		m.refreshViewport()
		return
	}
	if m.workflow == nil {
		return
	}
	for i := range m.workflow.items {
		if m.workflow.items[i].ID != ev.ID {
			continue
		}
		switch ev.Action {
		case "start":
			m.workflow.active = ev.ID
			m.workflow.items[i].Status = workflow.Running
		case "complete":
			if m.workflow.active == ev.ID {
				m.workflow.active = ""
			}
			m.workflow.items[i].Status = workflow.Done
		case "fail":
			if m.workflow.active == ev.ID {
				m.workflow.active = ""
			}
			m.workflow.items[i].Status = workflow.Failed
		case "skip":
			if m.workflow.active == ev.ID {
				m.workflow.active = ""
			}
			m.workflow.items[i].Status = workflow.Skipped
		}
		if ev.Detail != "" {
			m.workflow.items[i].Detail = ev.Detail
		}
		m.refreshViewport()
		return
	}
}

func (m *model) noteWorkflowActivity(detail string) {
	if m.workflow == nil || m.workflow.active == "" || strings.TrimSpace(detail) == "" {
		return
	}
	for i := range m.workflow.items {
		if m.workflow.items[i].ID == m.workflow.active {
			m.workflow.items[i].Detail = detail
			m.refreshViewport()
			return
		}
	}
}

func toolActivity(name string, args map[string]any) string {
	switch name {
	case "bash":
		if cmd, ok := args["command"].(string); ok && strings.TrimSpace(cmd) != "" {
			return "$ " + oneLine(cmd)
		}
	case "read_file", "write_file":
		if p, ok := args["path"].(string); ok && strings.TrimSpace(p) != "" {
			return name + " " + p
		}
	case "edit_file":
		if p, ok := args["path"].(string); ok && strings.TrimSpace(p) != "" {
			return "edit " + p
		}
	case "grep":
		if pat, ok := args["pattern"].(string); ok && strings.TrimSpace(pat) != "" {
			return "grep " + pat
		}
	case "glob":
		if pat, ok := args["pattern"].(string); ok && strings.TrimSpace(pat) != "" {
			return "glob " + pat
		}
	case "agent":
		if prompt, ok := args["prompt"].(string); ok && strings.TrimSpace(prompt) != "" {
			return "agent " + oneLine(prompt)
		}
	}
	return name
}

// handleStepEvent folds the supervisor's event stream into tree blocks:
// "start" places a node (a fresh block per top-level call unless the
// previous block is still the active tree), "done"/"fail" settle it, and
// everything else updates the node's live status line.
func (m *model) handleStepEvent(ev factory.Event) {
	switch ev.Ev.Kind {
	case "start":
		m.startTreeNode(ev)
	case "done", "fail":
		tb := m.treeIndex[ev.Node]
		if tb == nil {
			return
		}
		if n := tb.nodes[ev.Node]; n != nil && !n.done {
			n.done = true
			n.ok = ev.Ev.Kind == "done"
			n.elapsed = time.Since(n.startAt)
			n.lastLine = ""
			m.refreshViewport()
		}
	case "tool", "text", "error":
		tb := m.treeIndex[ev.Node]
		if tb == nil {
			return
		}
		if n := tb.nodes[ev.Node]; n != nil && !n.done {
			if line := strings.TrimSpace(ev.Ev.Body); line != "" {
				n.lastLine = line
				m.refreshViewport()
			}
		}
	}
}

// startTreeNode places a started node in a tree block. Top-level calls
// (children of the chat agent, node 0) root a block; deeper nodes attach
// under their parent's block wherever it lives.
func (m *model) startTreeNode(ev factory.Event) {
	if m.treeIndex == nil {
		m.treeIndex = map[int]*treeBlock{}
	}
	node := &treeNode{id: ev.Node, parent: ev.Parent, step: ev.Step, task: ev.Task, startAt: time.Now()}
	if ev.Parent == 0 {
		if m.activeTree == nil || len(m.blocks) == 0 || m.blocks[len(m.blocks)-1] != block(m.activeTree) {
			m.activeTree = newTreeBlock()
			m.appendBlock(m.activeTree)
		}
		m.activeTree.roots = append(m.activeTree.roots, ev.Node)
		m.activeTree.nodes[ev.Node] = node
		m.treeIndex[ev.Node] = m.activeTree
		m.refreshViewport()
		return
	}
	tb := m.treeIndex[ev.Parent]
	if tb == nil {
		return // parent unknown (e.g. events from a pre-resume session)
	}
	tb.nodes[ev.Node] = node
	tb.children[ev.Parent] = append(tb.children[ev.Parent], ev.Node)
	m.treeIndex[ev.Node] = tb
	m.refreshViewport()
}

func (m *model) appendTranscript(messages []llm.Message) {
	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleUser:
			var toolResults []llm.ContentBlock
			var textParts []string
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						textParts = append(textParts, block.Text)
					}
				case "image":
					textParts = append(textParts, "[image]")
				case "tool_result":
					toolResults = append(toolResults, block)
				}
			}
			if len(textParts) > 0 {
				m.blocks = append(m.blocks, userBlock{text: strings.Join(textParts, "\n")})
			}
			for _, block := range toolResults {
				m.blocks = append(m.blocks, toolResultBlock{text: block.Content, isError: block.IsError})
			}
		case llm.RoleAssistant:
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						m.blocks = append(m.blocks, textBlock{text: block.Text})
					}
				case "tool_use":
					m.blocks = append(m.blocks, toolCallBlock{name: block.Name, args: block.Input})
				}
			}
		}
	}
	m.refreshViewport()
}

func (m *model) startSend(text string, images []string) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.sendCancel = cancel
	return func() tea.Msg {
		_, err := m.ag.SendWith(ctx, text, images)
		if m.afterSend != nil {
			if saveErr := m.afterSend(); saveErr != nil && err == nil {
				err = saveErr
			}
		}
		return sendResultMsg{err: err}
	}
}

func (m *model) startTool(name string, input map[string]any) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.sendCancel = cancel
	return func() tea.Msg {
		m.ag.RunTool(ctx, name, input)
		return sendResultMsg{}
	}
}

// shortPaths renders attachment paths as just their base names for the inline
// notice, so a long absolute path doesn't blow out the line.
func shortPaths(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

func gitBranch() string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "no-git"
	}
	return strings.TrimSpace(string(out))
}
