package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/owainlewis/neo/internal/agent"
)

// Run starts the Bubble Tea chat TUI. It returns when the user quits.
func Run(ctx context.Context, ag *agent.Agent, model string) error {
	m, err := newModel(ctx, ag, model)
	if err != nil {
		return err
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
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
}

func newModel(ctx context.Context, ag *agent.Agent, modelTag string) (*model, error) {
	md, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		md = nil
	}

	ta := textarea.New()
	ta.Placeholder = "Ask neo anything…"
	ta.Prompt = "› "
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.Focus()

	vp := viewport.New(80, 20)
	vp.MouseWheelEnabled = true

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colAccent)

	cwd, _ := os.Getwd()
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, cwd); err == nil && !strings.HasPrefix(rel, "..") {
			cwd = "~/" + rel
		}
	}

	return &model{
		ctx:      ctx,
		ag:       ag,
		modelTag: modelTag,
		cwd:      cwd,
		branch:   gitBranch(),
		viewport: vp,
		input:    ta,
		spin:     sp,
		caption:  randomCaption(),
		md:       md,
	}, nil
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
			m.quitting = true
			return m, tea.Quit
		case "esc":
			// Soft interrupt: cancel the in-flight turn without quitting.
			if m.busy && m.sendCancel != nil {
				m.sendCancel()
			}
		case "enter":
			if m.busy {
				break
			}
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				break
			}
			m.input.Reset()
			m.appendBlock(userBlock{text: text})
			m.busy = true
			m.busySince = time.Now()
			m.caption = randomCaption()
			cmds = append(cmds, m.startSend(text))
		case "ctrl+l":
			m.blocks = nil
			m.refreshViewport()
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
		}

	case agentEventMsg:
		m.handleEvent(msg.ev)

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

func (m *model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return "loading…"
	}

	status := m.statusLine()
	footer := m.footerLine()
	inputBar := styInputBar.Width(m.width).Render(m.input.View())

	return lipgloss.JoinVertical(lipgloss.Left,
		m.viewport.View(),
		status,
		inputBar,
		footer,
	)
}

func (m *model) statusLine() string {
	if !m.busy {
		return styMuted.Render(" ready")
	}
	elapsed := time.Since(m.busySince).Round(time.Second)
	return " " + m.spin.View() + " " + styMuted.Render(m.caption+"…") +
		styDim.Render(fmt.Sprintf("  %s", elapsed))
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

func (m *model) layout() {
	inputHeight := 1 + 2 // textarea + top/bottom border
	chrome := inputHeight + 2 // status + footer lines
	vpH := m.height - chrome
	if vpH < 3 {
		vpH = 3
	}
	m.viewport.Width = m.width
	m.viewport.Height = vpH
	m.input.SetWidth(m.width - 2)
	if m.md != nil {
		// Re-create renderer at the new width so code blocks wrap nicely.
		if r, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
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
