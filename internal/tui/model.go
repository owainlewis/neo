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
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/session"
	"github.com/owainlewis/neo/internal/skills"
)

type Options struct {
	AfterSend       func() error
	PermissionMode  string
	SessionStore    *session.Store
	CurrentSession  *session.Session
	OnSessionResume func(*session.Session)
	ModelChoices    []ModelChoice
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
	sessions sessionBrowser
	models   modelBrowser

	// lastInputHeight is the textarea height the current layout was computed
	// for. When the textarea grows/shrinks (DynamicHeight), this lets us
	// detect the change and re-layout the viewport around it.
	lastInputHeight int

	blocks []block
	md     *glamour.TermRenderer

	busy        bool
	busySince   time.Time
	currentTool *toolCallBlock
	approval    *approvalState
	quitting    bool

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
		md:                md,
		skills:            sk,
		afterSend:         opts.AfterSend,
		permissionMode:    opts.PermissionMode,
		sessionStore:      opts.SessionStore,
		currentSessionID:  currentSessionID,
		currentSessionCWD: currentSessionCWD,
		onSessionResume:   opts.OnSessionResume,
		modelChoices:      normalizeModelChoices(modelTag, opts.ModelChoices),
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
			rawInput := text
			// Slash commands are parsed before the busy gate.
			if strings.HasPrefix(text, "/") {
				m.input.Reset()
				m.hideSlashPicker()
				m.layout()
				m.syncInputHeight()
				m.handleSlashCommand(text)
				break
			}
			// Chat text is suppressed while a turn is in flight.
			if m.busy {
				break
			}
			m.input.Reset()
			m.hideSlashPicker()
			m.layout()
			m.syncInputHeight()
			// Pull any dragged/pasted image paths out of the input; they become
			// attachments on the message, the rest stays as text.
			text, images := extractImagePaths(text)
			m.appendBlock(userBlock{text: rawInput})
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
			m.updateSlashPicker()
			m.syncInputHeight()
		case "up":
			if m.picker.visible {
				m.moveSlashPickerSelection(-1)
				break
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			m.updateSlashPicker()
			m.syncInputHeight()
		case "down":
			if m.picker.visible {
				m.moveSlashPickerSelection(1)
				break
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			m.updateSlashPicker()
			m.syncInputHeight()
		case "tab":
			if m.picker.visible && m.acceptSlashPicker(true) {
				m.syncInputHeight()
				m.layout()
				break
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			m.updateSlashPicker()
			m.syncInputHeight()
		case "ctrl+l":
			m.blocks = nil
			m.refreshViewport()
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			m.updateSlashPicker()
			m.syncInputHeight()
		}

	case agentEventMsg:
		m.handleEvent(msg.ev)
		// Tie the dot's color to whether a tool is currently running.
		if m.currentTool != nil {
			m.setDotColor(colDotTool)
		} else if m.busy {
			m.setDotColor(colDotThinking)
		}

	case approvalRequestMsg:
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

	status := m.statusLine()
	footer := m.footerLine()
	picker := m.slashPickerView()
	inputBar := styInputBar.Width(m.width).Render(m.input.View())

	parts := []string{
		m.viewport.View(),
		status,
		"",
		inputBar,
	}
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
		m.appendBlock(helpBlock{})
	case "/tools":
		m.appendBlock(toolsBlock{specs: m.ag.ToolSpecs()})
	case "/permissions":
		mode := m.permissionMode
		if mode == "" {
			mode = "ask"
		}
		m.appendBlock(noticeBlock{text: "permissions: " + mode})
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
		m.appendBlock(errorBlock{err: fmt.Errorf("unknown command: %s — try /help", cmd)})
	}
}

func slashCommandRequiresIdle(cmd string) bool {
	switch cmd {
	case "/clear", "/tokens", "/sessions", "/model":
		return true
	default:
		return false
	}
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
	}
	chrome := inputHeight + pickerHeight + 4 // status + footer lines + margin above/below input
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
	case agent.EventMaxTurnsReached:
		m.appendBlock(maxTurnsBlock{limit: e.MaxTurns})
	case agent.EventDone:
		// handled when sendResultMsg arrives
	}
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
