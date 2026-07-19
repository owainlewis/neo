package tui

import (
	"context"
	"errors"
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
	"github.com/owainlewis/neo/internal/factory"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/permission"
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
	OnSessionResume func(*session.Session) error
	ModelChoices    []ModelChoice
	Provider        string
	ModelSwitcher   func(ModelChoice) error
	StepEvents      <-chan factory.Event
	WorkflowEvents  <-chan workflow.Event
	Verbose         bool
}

type Option func(*Options)

func WithAfterSend(fn func() error) Option {
	return func(opts *Options) { opts.AfterSend = fn }
}

func WithPermissionMode(mode string) Option {
	return func(opts *Options) { opts.PermissionMode = mode }
}

func WithSessions(store *session.Store, current *session.Session, onResume func(*session.Session) error) Option {
	return func(opts *Options) {
		opts.SessionStore = store
		opts.CurrentSession = current
		opts.OnSessionResume = onResume
	}
}

func WithModelChoices(choices []ModelChoice) Option {
	return func(opts *Options) { opts.ModelChoices = choices }
}

// WithModelSwitcher enables provider-aware model selection. The callback must
// finish the backend switch before returning; the TUI updates its labels only
// after it succeeds.
func WithModelSwitcher(provider string, choices []ModelChoice, fn func(ModelChoice) error) Option {
	return func(opts *Options) {
		opts.Provider = provider
		opts.ModelChoices = choices
		opts.ModelSwitcher = fn
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

// WithVerbose controls tool activity rendering: false (the default) shows live
// activity and concise completed receipts while hiding successful tool result
// bodies; true restores full tool call/result cards. Errors always render in
// full regardless of this setting.
func WithVerbose(verbose bool) Option {
	return func(opts *Options) { opts.Verbose = verbose }
}

// Run starts the Bubble Tea chat TUI. It returns when the user quits. sk is the
// loaded skill set used for $name expansion (nil when the feature is off).
func Run(ctx context.Context, ag *agent.Agent, model, version string, sk []skills.Skill, options ...Option) error {
	logx.Debug("tui start", "model", model, "skills", len(sk))
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
	if err != nil {
		logx.Debug("tui exit", "error", err.Error())
	} else {
		logx.Debug("tui exit", "error", "")
	}
	return err
}

type sendResultMsg struct{ err error }
type agentEventMsg struct{ ev agent.Event }
type stepEventMsg struct{ ev factory.Event }
type workflowEventMsg struct{ ev workflow.Event }
type branchMsg struct{ branch string }
type approvalRequestMsg struct {
	req   agent.ApprovalRequest
	reply chan bool
}

type approvalState struct {
	req      agent.ApprovalRequest
	reply    chan bool
	expanded bool
}

type turnStats struct {
	tools    int
	errors   int
	workflow bool
	direct   bool
}

type queuedTurn struct {
	displayText string
	agentText   string
	images      []string
}

type model struct {
	ctx         context.Context
	ag          *agent.Agent
	modelTag    string
	providerTag string

	cwd    string
	branch string

	width, height int

	viewport viewport.Model
	input    textarea.Model
	spin     spinner.Model
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

	busy            bool
	busySince       time.Time
	currentTool     *toolCallBlock
	workflow        *workflowBlock
	workflowVisible bool
	turn            turnStats
	activeTree      *treeBlock         // block receiving new top-level subagent trees
	treeIndex       map[int]*treeBlock // supervisor node id -> the block holding it
	approval        *approvalState
	// allow holds the rules the user granted via "always allow" during this
	// session. It is consulted before prompting, so a granted tool/command
	// stops asking. It is intentionally not persisted.
	allow    permission.Allowlist
	quitting bool

	// cancel for the currently in-flight Send, if any.
	sendCancel      context.CancelFunc
	steer           func(string) bool
	pendingSteering []string
	queued          *queuedTurn

	// mdStyleName is the glamour style chosen at startup. We re-use it when
	// recreating the renderer on resize so we never re-probe the terminal
	// from inside raw mode (which leaks the OSC 11 reply into the textarea).
	mdStyleName string

	// skills drives $name expansion of the user's input and /name skill
	// invocations before a turn is sent.
	skills []skills.Skill

	afterSend         func() error
	permissionMode    string
	sessionStore      *session.Store
	currentSessionID  string
	currentSessionCWD string
	onSessionResume   func(*session.Session) error
	modelChoices      []ModelChoice
	modelSwitcher     func(ModelChoice) error
	verbose           bool
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
	// (or shift+wheel) event that would otherwise slide the whole transcript.
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

	providerTag := strings.TrimSpace(opts.Provider)
	m := &model{
		ctx:               ctx,
		ag:                ag,
		modelTag:          modelTag,
		providerTag:       providerTag,
		mdStyleName:       styleName,
		cwd:               cwd,
		branch:            branch,
		viewport:          vp,
		input:             ta,
		spin:              sp,
		files:             newFilePicker(workspace.Root(absCWD)),
		md:                md,
		skills:            sk,
		afterSend:         opts.AfterSend,
		permissionMode:    opts.PermissionMode,
		sessionStore:      opts.SessionStore,
		currentSessionID:  currentSessionID,
		currentSessionCWD: currentSessionCWD,
		onSessionResume:   opts.OnSessionResume,
		modelChoices:      normalizeModelChoices(providerTag, modelTag, opts.ModelChoices),
		modelSwitcher:     opts.ModelSwitcher,
		verbose:           opts.Verbose,
		steer:             ag.Steer,
	}
	// Welcome banner shown once at the top of scrollback.
	m.blocks = append(m.blocks, splashBlock{
		version: version,
		model:   backendLabel(providerTag, modelTag),
		cwd:     cwd,
		branch:  branch,
		tagline: randomTagline(),
	})
	m.appendTranscript(ag.Transcript())
	return m, nil
}

func (m *model) Init() tea.Cmd {
	return m.spin.Tick
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
			return m, m.handleApprovalKey(msg)
		}
		cmds = append(cmds, m.handleKey(msg))

	case tea.PasteMsg:
		if m.perms.visible || m.models.visible || m.sessions.visible || m.approval != nil {
			break
		}
		cmds = append(cmds, m.updateInput(msg))

	case agentEventMsg:
		m.handleEvent(msg.ev)
		// Tie the dot's color to whether a tool is currently running.
		if m.currentTool != nil {
			m.setDotColor(colDotTool)
		} else if m.busy {
			m.setDotColor(colDotThinking)
		}
		m.layout()

	case approvalRequestMsg:
		logx.Debug("tui approval requested", "tool", msg.req.ToolName, "reason", msg.req.Reason)
		req := permission.Request{ToolName: msg.req.ToolName, Args: msg.req.Args}
		if m.allow.Allows(req) {
			msg.reply <- true
			break
		}
		m.approval = &approvalState{req: msg.req, reply: msg.reply}
		m.appendBlock(approvalBlock{req: msg.req})
		m.layout()

	case sendResultMsg:
		elapsed := time.Since(m.busySince)
		m.busy = false
		m.currentTool = nil
		m.layout()
		if m.sendCancel != nil {
			m.sendCancel()
			m.sendCancel = nil
		}
		canceled := errors.Is(msg.err, context.Canceled)
		if canceled {
			m.appendBlock(noticeBlock{text: "turn canceled"})
		} else if errors.Is(msg.err, agent.ErrMaxTurns) {
			// EventMaxTurnsReached already appended a maxTurnsBlock
			// describing this turn; don't also emit a result summary.
		} else if msg.err != nil {
			m.appendBlock(errorBlock{err: msg.err})
		} else if summary, ok := m.resultSummary(msg.err, elapsed); ok {
			m.appendBlock(summary)
		}
		cmds = append(cmds, refreshBranch())
		if msg.err != nil {
			var recovered []string
			if len(m.pendingSteering) > 0 {
				recovered = append(recovered, m.pendingSteering...)
				m.appendBlock(noticeBlock{text: "unapplied steering returned to the composer"})
			}
			if m.queued != nil {
				recovered = append(recovered, m.queued.displayText)
				m.queued = nil
				m.appendBlock(noticeBlock{text: "queued follow-up returned to the composer"})
			}
			m.restoreInput(recovered...)
		}
		m.pendingSteering = nil
		if m.queued != nil { // a successful turn starts its single follow-up
			queued := m.queued
			m.queued = nil
			m.appendBlock(noticeBlock{text: "starting queued follow-up"})
			cmds = append(cmds, m.submitUserTurn(queued.displayText, queued.agentText, queued.images))
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

	case branchMsg:
		if strings.TrimSpace(msg.branch) != "" {
			m.branch = msg.branch
		}

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

	parts := []string{m.viewport.View(), ""}
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
// every frame: alt screen, cell-motion mouse reporting, and a request for
// keyboard enhancements. Mouse reporting enables wheel and trackpad scrolling;
// common terminals keep text selection available with shift+drag.
// ReportAlternateKeys asks terminals that speak the Kitty
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

const defaultPlaceholder = "Ask neo anything…   ↩ send"

func (m *model) submitUserTurn(displayText, agentText string, images []string) tea.Cmd {
	return m.submitUserTurnWithSkillExpansion(displayText, agentText, images, true)
}

func (m *model) steerActiveTurn(displayText, agentText string) {
	sent, used := skills.Expand(agentText, m.skills)
	if m.steer == nil || !m.steer(sent) {
		m.appendBlock(errorBlock{err: fmt.Errorf("this operation cannot be steered; use ctrl+enter to queue a follow-up")})
		return
	}
	m.resetInput()
	m.appendBlock(userBlock{text: displayText})
	m.pendingSteering = append(m.pendingSteering, displayText)
	if len(used) > 0 {
		m.appendBlock(noticeBlock{text: "applied skill: " + strings.Join(used, ", ")})
	}
	m.appendBlock(noticeBlock{text: "steering current turn"})
}

func (m *model) queueFollowUp(displayText string) {
	if m.queued != nil {
		m.appendBlock(errorBlock{err: fmt.Errorf("a follow-up is already queued")})
		return
	}
	if strings.HasPrefix(displayText, "/") || strings.HasPrefix(displayText, "!") {
		m.appendBlock(errorBlock{err: fmt.Errorf("commands cannot be queued; wait for the turn to finish or use $skill in a chat message")})
		return
	}
	agentText, images := extractImagePaths(displayText)
	m.queued = &queuedTurn{displayText: displayText, agentText: agentText, images: images}
	m.resetInput()
	m.appendBlock(noticeBlock{text: "queued next: " + oneLine(displayText)})
}

func (m *model) restoreInput(texts ...string) {
	current := strings.TrimSpace(m.input.Value())
	if current != "" {
		texts = append(texts, current)
	}
	if len(texts) == 0 {
		return
	}
	m.input.SetValue(strings.Join(texts, "\n"))
	m.syncInputHeight()
	m.layout()
}

func (m *model) submitUserTurnWithSkillExpansion(displayText, agentText string, images []string, expandSkillRefs bool) tea.Cmd {
	m.clearTerminalWorkflow()
	m.appendBlock(userBlock{text: displayText})
	if len(images) > 0 {
		m.appendBlock(noticeBlock{text: "attached image: " + strings.Join(shortPaths(images), ", ")})
	}
	sent := agentText
	if expandSkillRefs {
		var used []string
		sent, used = skills.Expand(agentText, m.skills)
		if len(used) > 0 {
			m.appendBlock(noticeBlock{text: "applied skill: " + strings.Join(used, ", ")})
		}
	}
	m.busy = true
	m.busySince = time.Now()
	m.turn = turnStats{}
	m.setDotColor(colDotThinking)
	return m.startSend(sent, images)
}

// handleSlashCommand parses slash commands. Called only when input begins
// with '/'.
func (m *model) handleSlashCommand(line string) tea.Cmd {
	parts := strings.Fields(line)
	cmd := parts[0]
	if m.busy && slashCommandRequiresIdle(cmd) {
		m.appendBlock(errorBlock{err: fmt.Errorf("%s is unavailable while a turn is running", cmd)})
		return nil
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
		if sk, ok := m.slashSkill(cmd); ok {
			if m.busy {
				m.appendBlock(errorBlock{err: fmt.Errorf("%s is unavailable while a turn is running", cmd)})
				return nil
			}
			args := strings.TrimSpace(strings.TrimPrefix(line, cmd))
			expanded := skills.ExpandInvocation(sk, args)
			send := m.submitUserTurnWithSkillExpansion(line, expanded, nil, false)
			m.appendBlock(noticeBlock{text: "applied skill: " + sk.Name})
			return send
		}
		m.appendBlock(errorBlock{err: fmt.Errorf("unknown command: %s — try /help", cmd)})
	}
	return nil
}

func slashCommandRequiresIdle(cmd string) bool {
	switch cmd {
	case "/clear", "/tokens", "/sessions", "/model", "/permissions":
		return true
	default:
		return false
	}
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
	m.turn = turnStats{direct: true}
	m.setDotColor(colDotThinking)
	return m.startTool("bash", map[string]any{"command": command})
}

func (m *model) finishApproval(ok bool) {
	if m.approval == nil {
		return
	}
	m.approval.reply <- ok
	if ok {
		logx.Debug("tui approval answered", "tool", m.approval.req.ToolName, "approved", true)
		m.appendBlock(noticeBlock{text: "approved " + m.approval.req.ToolName})
	} else {
		logx.Debug("tui approval answered", "tool", m.approval.req.ToolName, "approved", false)
		m.appendBlock(noticeBlock{text: "denied " + m.approval.req.ToolName})
	}
	m.approval = nil
}

func (m *model) toggleApprovalPreview() bool {
	if m.approval == nil || !approvalPreviewIsTruncated(m.approval.req.Preview) {
		return false
	}
	m.approval.expanded = !m.approval.expanded
	for i := len(m.blocks) - 1; i >= 0; i-- {
		b, ok := m.blocks[i].(approvalBlock)
		if !ok {
			continue
		}
		b.expanded = m.approval.expanded
		m.blocks[i] = b
		m.refreshViewport()
		return true
	}
	return false
}

func (m *model) resultSummary(err error, elapsed time.Duration) (resultSummaryBlock, bool) {
	if !m.turn.direct && m.turn.tools == 0 && !m.turn.workflow {
		return resultSummaryBlock{}, false
	}
	maxTurns := errors.Is(err, agent.ErrMaxTurns)
	failed := !maxTurns && (err != nil || m.turn.errors > 0)
	label := "Done"
	if maxTurns {
		label = "Paused"
	} else if failed {
		label = "Finished with issues"
	}
	parts := []string{}
	if m.turn.tools > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", m.turn.tools, plural("tool", m.turn.tools)))
	}
	if m.turn.workflow {
		parts = append(parts, "plan updated")
	}
	if m.turn.direct {
		parts = append(parts, "command complete")
	}
	if maxTurns {
		parts = append(parts, "reply to continue")
	}
	return resultSummaryBlock{label: label, detail: strings.Join(parts, " · "), elapsed: elapsed, failed: failed}, true
}

func plural(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
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

	fullHint := "↩ steer · ctrl+↩ queue · esc interrupt"
	compactHint := "esc interrupt"
	if m.approval != nil {
		fullHint = "esc to deny"
		compactHint = "esc deny"
	} else if m.queued != nil && m.turn.direct {
		fullHint = "next queued · esc interrupt"
		compactHint = "queued · esc interrupt"
	} else if m.queued != nil {
		fullHint = "next queued · ↩ steer · esc interrupt"
		compactHint = "queued · esc interrupt"
	} else if m.turn.direct {
		fullHint = "ctrl+↩ queue · esc interrupt"
	}
	if m.workflow != nil {
		planHint := "tab plan"
		if m.workflowVisible {
			planHint = "tab hide plan"
		}
		fullHint = planHint + " · " + fullHint
		compactHint = planHint + " · " + compactHint
	}
	activity := m.statusActivity()
	prefix := " " + m.spin.View() + " "
	timing := " · " + formatElapsedCompact(elapsed)
	hint := statusHintForWidth(m.width-lipgloss.Width(prefix)-lipgloss.Width(timing), fullHint, compactHint)
	suffix := timing
	if hint != "" {
		suffix += "   " + hint
	}
	activityWidth := max(m.width-lipgloss.Width(prefix)-lipgloss.Width(suffix), 1)
	line := prefix + styLabel.Render(truncate(activity, activityWidth)) + styDim.Render(suffix)
	return truncate(line, max(m.width, 1))
}

func statusHintForWidth(available int, full, compact string) string {
	const minimumActivityWidth = 16
	if lipgloss.Width(full)+3+minimumActivityWidth <= available {
		return full
	}
	if lipgloss.Width(compact)+3+minimumActivityWidth <= available {
		return compact
	}
	return ""
}

func (m *model) statusActivity() string {
	if m.approval != nil {
		return "Waiting for approval"
	}
	parts := []string{}
	if workflow := m.workflowProgress(); workflow != "" {
		parts = append(parts, workflow)
	}
	if m.currentTool != nil {
		parts = append(parts, capitalize(toolVerb(m.currentTool.name, m.currentTool.args)))
	}
	if len(parts) == 0 {
		return "Thinking"
	}
	return strings.Join(parts, " · ")
}

func (m *model) workflowProgress() string {
	if m.workflow == nil || len(m.workflow.items) == 0 {
		return ""
	}
	for i, item := range m.workflow.items {
		if item.Status == workflow.Running {
			return fmt.Sprintf("%d/%d %s", i+1, len(m.workflow.items), oneLine(item.Text))
		}
	}
	done, failed, skipped := workflowCounts(m.workflow.items)
	finished := done + failed + skipped
	if finished == len(m.workflow.items) {
		return fmt.Sprintf("%d/%d Plan complete", finished, len(m.workflow.items))
	}
	title := strings.TrimSpace(m.workflow.title)
	if title == "" {
		title = "Plan"
	}
	return fmt.Sprintf("%d/%d %s", finished, len(m.workflow.items), oneLine(title))
}

func formatElapsedCompact(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	return fmt.Sprintf("%dm %02ds", seconds/60, seconds%60)
}

func capitalize(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func formatElapsed(d time.Duration) string {
	if d > 0 && d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d / time.Minute)
	s := int(d/time.Second) % 60
	return fmt.Sprintf("%dm%02ds", m, s)
}

func (m *model) footerLine() string {
	left := fmt.Sprintf("%s (%s)", m.cwd, m.branch)
	right := backendLabel(m.providerTag, m.modelTag)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return styFooter.Render(left + strings.Repeat(" ", gap) + right)
}

// inputMaxRows caps how tall the input grows; beyond that the textarea
// scrolls internally.
const inputMaxRows = 8

// transcriptProgressGapHeight reserves breathing room between scrollable
// output and the fixed workflow/status area below it.
const transcriptProgressGapHeight = 1

const minimumTranscriptHeight = 1

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
	followOutput := m.viewport.AtBottom()
	previousOffset := m.viewport.YOffset()
	workflowHeight := m.workflowPanelHeight()
	chrome := m.fixedChromeHeight() + workflowHeight
	vpH := m.height - chrome
	if vpH < minimumTranscriptHeight {
		vpH = minimumTranscriptHeight
	}
	m.viewport.SetWidth(m.width)
	m.viewport.SetHeight(vpH)
	// A shorter viewport otherwise looks manually scrolled even when it was
	// following the bottom before surrounding UI grew.
	if followOutput {
		m.viewport.GotoBottom()
	} else {
		m.viewport.SetYOffset(previousOffset)
	}
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

func (m *model) statusLineHeight() int {
	return 1
}

func (m *model) fixedChromeHeight() int {
	return m.baseChromeHeight() + m.pickerPanelHeight()
}

func (m *model) baseChromeHeight() int {
	inputHeight := m.input.Height() + 2 // textarea body + top/bottom padding
	return inputHeight + transcriptProgressGapHeight + 3 + m.statusLineHeight()
}

func (m *model) pickerPanelHeight() int {
	return lipgloss.Height(m.inlinePickerView())
}

func (m *model) maxInlinePickerRows() int {
	return max(m.height-m.baseChromeHeight()-minimumTranscriptHeight, 0)
}

func (m *model) appendBlock(b block) {
	m.blocks = append(m.blocks, b)
	m.refreshViewport()
}

func (m *model) toggleLatestToolResultExpansion() bool {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		b, ok := m.blocks[i].(toolResultBlock)
		if !ok || !b.isTruncated() {
			continue
		}
		b.expanded = !b.expanded
		m.blocks[i] = b
		m.refreshViewport()
		return true
	}
	return false
}

func (m *model) workflowPanelView() string {
	if m.workflow == nil || !m.workflowVisible {
		return ""
	}
	panel := m.workflow.render(m.width, nil)
	lines := strings.Split(panel, "\n")
	maxLines := m.maxWorkflowPanelLines()
	if maxLines <= 0 {
		return ""
	}
	if len(lines) <= maxLines {
		return panel
	}
	if maxLines == 1 {
		return truncate(lines[0]+styMuted.Render("  … more"), max(m.width, 1))
	}
	visible := append([]string(nil), lines[:maxLines]...)
	hidden := len(lines) - (maxLines - 1)
	visible[maxLines-1] = styMuted.Render(fmt.Sprintf("… %d more", hidden))
	return strings.Join(visible, "\n")
}

func (m *model) maxWorkflowPanelLines() int {
	// Expanded plans yield rows to the transcript and retain their trailing
	// margin before the fixed status line.
	return max(m.height-m.fixedChromeHeight()-minimumTranscriptHeight-1, 0)
}

func (m *model) workflowPanelHeight() int {
	panel := m.workflowPanelView()
	if panel == "" {
		return 0
	}
	return strings.Count(panel, "\n") + 2 // panel lines plus one-line margin before status
}

func (m *model) workflowTerminal() bool {
	if m.workflow == nil || len(m.workflow.items) == 0 {
		return false
	}
	for _, item := range m.workflow.items {
		switch item.Status {
		case workflow.Done, workflow.Failed, workflow.Skipped:
			continue
		default:
			return false
		}
	}
	return true
}

func (m *model) clearTerminalWorkflow() {
	if !m.workflowTerminal() {
		return
	}
	m.workflow = nil
	m.workflowVisible = false
	m.layout()
}

func (m *model) refreshViewport() {
	if m.width == 0 {
		return
	}
	followOutput := m.viewport.AtBottom()
	previousOffset := m.viewport.YOffset()
	var sb strings.Builder
	for i, b := range m.blocks {
		if i > 0 {
			sb.WriteString(blockSeparator(m.blocks[i-1], b))
		}
		sb.WriteString(b.render(m.width, m.md))
		sb.WriteString("\n")
	}
	m.viewport.SetContent(sb.String())
	if followOutput {
		m.viewport.GotoBottom()
	} else {
		m.viewport.SetYOffset(previousOffset)
	}
}

func blockSeparator(previous, next block) string {
	previousTool, previousOK := previous.(toolCallBlock)
	nextTool, nextOK := next.(toolCallBlock)
	if previousOK && nextOK && !previousTool.verbose && !nextTool.verbose {
		return ""
	}
	return "\n"
}

func (m *model) handleEvent(e agent.Event) {
	switch e.Kind {
	case agent.EventAssistantText:
		m.activeTree = nil // assistant commentary splits subagent trees
		if strings.TrimSpace(e.Text) != "" {
			m.appendBlock(textBlock{text: e.Text})
		}
	case agent.EventAssistantCommentary:
		m.activeTree = nil
		if strings.TrimSpace(e.Text) != "" {
			m.appendBlock(thinkingBlock{text: e.Text})
		}
	case agent.EventSteeringApplied:
		if len(m.pendingSteering) > 0 {
			m.pendingSteering = m.pendingSteering[1:]
		}
	case agent.EventToolCall:
		if e.Name == "workflow" {
			m.turn.workflow = true
			// The workflow tool mutates the checklist through workflowEventMsg;
			// don't show a duplicate generic tool card.
			break
		}
		m.turn.tools++
		m.noteWorkflowActivity(capitalize(toolVerb(e.Name, e.Args)))
		tc := toolCallBlock{name: e.Name, args: e.Args, startAt: time.Now(), verbose: m.verbose}
		m.currentTool = &tc
		if e.Name == "agent" {
			// The supervisor's "start" event draws this call as a tree
			// node; no generic tool card.
			break
		}
		m.activeTree = nil
		if m.verbose {
			m.appendBlock(tc)
		}
	case agent.EventToolResult:
		if e.Name == "workflow" {
			// Successful workflow calls are represented by the checklist UI, but
			// failures may not produce a workflow event and must remain visible.
			if e.IsError {
				m.turn.errors++
				m.appendBlock(toolResultBlock{name: e.Name, text: e.Text, isError: true})
			}
			break
		}
		elapsed := time.Duration(0)
		if m.currentTool != nil {
			elapsed = time.Since(m.currentTool.startAt)
		}
		completedTool := m.currentTool
		m.currentTool = nil
		if e.IsError {
			m.turn.errors++
		}
		if e.Name == "agent" {
			// Success renders in the tree. Failures/denials keep an error card
			// so the output is inspectable.
			if e.IsError || !runStepOK(e.Text) {
				m.appendBlock(toolResultBlock{name: e.Name, text: e.Text, isError: true, elapsed: elapsed})
			}
			break
		}
		if !m.verbose && !e.IsError && completedTool != nil {
			completedTool.elapsed = elapsed
			m.appendBlock(*completedTool)
		}
		// In concise mode, routine successful result bodies add no scannable
		// information beyond the compact receipt, so only errors render.
		// Direct ! commands are user-requested output, not intermediate agent
		// activity, and must remain visible in either mode.
		if m.verbose || e.IsError || m.turn.direct {
			m.appendBlock(toolResultBlock{
				name:    e.Name,
				text:    e.Text,
				isError: e.IsError,
				elapsed: elapsed,
			})
		}
	case agent.EventError:
		m.appendBlock(errorBlock{err: e.Err})
	case agent.EventMaxTurnsReached:
		m.appendBlock(maxTurnsBlock{limit: e.MaxTurns})
	case agent.EventDone:
		// handled when sendResultMsg arrives
	}
}

func (m *model) appendTranscript(messages []llm.Message) {
	failedToolUses := failedTranscriptToolUseOccurrences(messages)
	for messageIndex, msg := range messages {
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
				if m.verbose || block.IsError {
					m.blocks = append(m.blocks, toolResultBlock{text: block.Content, isError: block.IsError})
				}
			}
		case llm.RoleAssistant:
			hasTools := hasTranscriptToolUse(msg.Content)
			for blockIndex, block := range msg.Content {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						if hasTools {
							m.blocks = append(m.blocks, thinkingBlock{text: block.Text})
						} else {
							m.blocks = append(m.blocks, textBlock{text: block.Text})
						}
					}
				case "tool_use":
					occurrence := transcriptToolUseOccurrence{message: messageIndex, block: blockIndex}
					if !m.verbose && failedToolUses[occurrence] {
						continue
					}
					m.blocks = append(m.blocks, toolCallBlock{name: block.Name, args: block.Input, verbose: m.verbose})
				}
			}
		}
	}
	m.refreshViewport()
}

type transcriptToolUseOccurrence struct {
	message int
	block   int
}

func failedTranscriptToolUseOccurrences(messages []llm.Message) map[transcriptToolUseOccurrence]bool {
	failed := make(map[transcriptToolUseOccurrence]bool)
	pending := make(map[string][]transcriptToolUseOccurrence)
	for messageIndex, msg := range messages {
		for blockIndex, block := range msg.Content {
			switch block.Type {
			case "tool_use":
				occurrence := transcriptToolUseOccurrence{message: messageIndex, block: blockIndex}
				pending[block.ID] = append(pending[block.ID], occurrence)
			case "tool_result":
				queue := pending[block.ToolUseID]
				if len(queue) == 0 {
					continue
				}
				occurrence := queue[0]
				pending[block.ToolUseID] = queue[1:]
				if block.IsError {
					failed[occurrence] = true
				}
			}
		}
	}
	return failed
}

func hasTranscriptToolUse(content []llm.ContentBlock) bool {
	for _, block := range content {
		if block.Type == "tool_use" {
			return true
		}
	}
	return false
}

func (m *model) startSend(text string, images []string) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.sendCancel = cancel
	logx.Debug("tui send start", "images", len(images), "text", logx.SafeString(text, 240))
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
	logx.Debug("tui tool start", "name", name, "args", logx.SafeAny(input))
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

func refreshBranch() tea.Cmd {
	return func() tea.Msg {
		return branchMsg{branch: gitBranch()}
	}
}

func gitBranch() string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "no-git"
	}
	return strings.TrimSpace(string(out))
}
