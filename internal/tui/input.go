package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/owainlewis/neo/internal/logx"
)

func (m *model) handleKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	quitArmed, cancelArmed := m.quitArmed, m.cancelArmed
	m.disarm()
	switch {
	case key == "ctrl+c":
		return m.handleInterrupt(quitArmed)
	case key == "ctrl+d" && m.input.Value() == "":
		return m.requestQuit()
	}
	if m.quitPending {
		return nil
	}
	switch key {
	case "esc":
		m.handleEscape(cancelArmed)
	case "ctrl+enter":
		if m.busy {
			if text := strings.TrimSpace(m.input.Value()); text != "" {
				m.queueFollowUp(text)
			}
		}
	case "enter":
		return m.handleEnter()
	case "shift+enter", "alt+enter", "ctrl+j":
		m.input.InsertString("\n")
		m.updateInlinePickers()
		m.syncInputHeight()
	case "up":
		if !m.moveInlinePicker(-1) {
			return m.updateInput(msg)
		}
	case "down":
		if !m.moveInlinePicker(1) {
			return m.updateInput(msg)
		}
	case "pgup":
		m.viewport.PageUp()
	case "pgdown":
		m.viewport.PageDown()
	// Line-at-a-time scrolling for terminals (or users) without a wheel. The
	// bare arrows belong to the composer and its pickers.
	case "shift+up":
		m.viewport.ScrollUp(1)
	case "shift+down":
		m.viewport.ScrollDown(1)
	case "shift+home":
		m.viewport.GotoTop()
	case "shift+end":
		m.viewport.GotoBottom()
	case "tab":
		if m.acceptInlinePicker(true) {
			break
		}
		return m.updateInput(msg)
	case "ctrl+l":
		m.blocks = nil
		m.rendered = nil
		m.refreshViewport()
	case "ctrl+o":
		m.toggleLatestToolResultExpansion()
	default:
		return m.updateInput(msg)
	}
	return nil
}

func (m *model) handleApprovalKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		logx.Debug("tui quit during approval", "tool", m.approval.req.ToolName)
		m.finishApproval(false)
		return m.requestQuit()
	case "y", "Y":
		m.approval.selected = 'y'
		m.layout()
	case "n", "N":
		m.approval.selected = 'n'
		m.layout()
	case "enter":
		switch m.approval.selected {
		case 'y':
			m.finishApproval(true)
		case 'n':
			m.finishApproval(false)
		}
	case "pgup":
		m.approval.selected = 0
		m.viewport.PageUp()
	case "pgdown":
		m.approval.selected = 0
		m.viewport.PageDown()
	case "shift+up":
		m.approval.selected = 0
		m.viewport.ScrollUp(1)
	case "shift+down":
		m.approval.selected = 0
		m.viewport.ScrollDown(1)
	case "esc":
		m.finishApproval(false)
	default:
		m.approval.selected = 0
	}
	return nil
}

func (m *model) clearApprovalSelection() {
	if m.approval != nil && m.approval.selected != 0 {
		m.approval.selected = 0
		m.layout()
	}
}

func (m *model) requestQuit() tea.Cmd {
	logx.Debug("tui quit requested", "busy", m.busy, "pending", m.quitPending)
	if m.quitPending {
		m.quitting = true
		return tea.Quit
	}
	if m.busy {
		m.quitPending = true
		if m.sendCancel != nil {
			m.sendCancel()
		}
		m.appendBlock(noticeBlock{text: "canceling turn and saving session; press ctrl+c again to force quit"})
		return nil
	}
	if m.persistenceErr != nil && m.afterSend != nil {
		m.quitPending = true
		m.appendBlock(noticeBlock{text: "retrying session save; press ctrl+c again to force quit"})
		afterSend := m.afterSend
		return func() tea.Msg {
			_, err := runPersistence(afterSend)
			return persistenceRetryResultMsg{err: err}
		}
	}
	m.quitting = true
	return tea.Quit
}

// handleInterrupt makes an idle ctrl+c safe to press by reflex: it clears a
// draft, and on an empty composer it needs a second press to quit. While a
// turn runs or a quit is already pending, requestQuit owns the sequence.
func (m *model) handleInterrupt(armed bool) tea.Cmd {
	if m.busy || m.quitPending || armed {
		return m.requestQuit()
	}
	if m.input.Value() != "" {
		m.resetInput()
		return nil
	}
	m.quitArmed = true
	m.appendBlock(noticeBlock{text: "press ctrl+c again to quit"})
	return nil
}

// disarm drops any pending ctrl+c or esc confirmation, so a later press needs
// a fresh first press.
func (m *model) disarm() {
	m.quitArmed = false
	m.cancelArmed = false
}

func (m *model) handleEscape(armed bool) {
	if m.files.visible {
		m.dismissFilePicker()
		m.layout()
		return
	}
	if m.picker.visible {
		m.dismissSlashPicker()
		m.layout()
		return
	}
	if !m.busy || m.sendCancel == nil {
		return
	}
	if !armed {
		m.cancelArmed = true
		m.appendBlock(noticeBlock{text: "press esc again to cancel the turn"})
		return
	}
	logx.Debug("tui send canceled", "mode", "soft_interrupt")
	m.sendCancel()
}

func (m *model) handleEnter() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	if m.acceptInlinePicker(false) {
		return nil
	}
	if strings.HasPrefix(text, "/") {
		m.resetInput()
		return m.handleSlashCommand(text)
	}
	if strings.HasPrefix(text, "!") {
		m.resetInput()
		return m.handleBangCommand(text)
	}
	if m.busy {
		m.steerActiveTurn(text, text)
		return nil
	}
	m.resetInput()
	agentText, images := extractImagePaths(text)
	return m.submitUserTurn(text, agentText, images)
}

func (m *model) updateInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateInlinePickers()
	m.syncInputHeight()
	return cmd
}

func (m *model) resetInput() {
	m.input.Reset()
	m.hideSlashPicker()
	m.hideFilePicker()
	m.layout()
	m.syncInputHeight()
}

func (m *model) moveInlinePicker(delta int) bool {
	if m.files.visible {
		m.moveFilePickerSelection(delta)
		return true
	}
	if m.picker.visible {
		m.moveSlashPickerSelection(delta)
		return true
	}
	return false
}

func (m *model) acceptInlinePicker(forceSlash bool) bool {
	var accepted bool
	if forceSlash {
		accepted = m.files.visible && m.acceptFilePicker() || m.picker.visible && m.acceptSlashPicker(true)
	} else {
		accepted = m.picker.visible && m.acceptSlashPicker(false) || m.files.visible && m.acceptFilePicker()
	}
	if !accepted {
		return false
	}
	m.syncInputHeight()
	m.layout()
	return true
}
