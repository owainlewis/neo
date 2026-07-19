package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/permission"
)

func (m *model) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		logx.Debug("tui quit requested", "busy", m.busy)
		if m.sendCancel != nil {
			m.sendCancel()
		}
		m.quitting = true
		return tea.Quit
	case "esc":
		m.handleEscape()
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
	case "tab":
		if m.acceptInlinePicker(true) {
			break
		}
		if m.workflow != nil {
			m.workflowVisible = !m.workflowVisible
			m.layout()
			break
		}
		return m.updateInput(msg)
	case "ctrl+l":
		m.blocks = nil
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
		if m.sendCancel != nil {
			m.sendCancel()
		}
		m.quitting = true
		return tea.Quit
	case "y", "Y":
		m.finishApproval(true)
	case "a", "A":
		req := permission.Request{ToolName: m.approval.req.ToolName, Args: m.approval.req.Args}
		rule := permission.RuleFor(req)
		m.allow.Add(rule)
		m.finishApproval(true)
		m.appendBlock(noticeBlock{text: "won't ask again for " + rule.Label() + " this session"})
	case "ctrl+o":
		m.toggleApprovalPreview()
	case "pgup":
		m.viewport.PageUp()
	case "pgdown":
		m.viewport.PageDown()
	case "n", "N", "esc":
		m.finishApproval(false)
	}
	return nil
}

func (m *model) handleEscape() {
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
	if m.busy && m.sendCancel != nil {
		logx.Debug("tui send canceled", "mode", "soft_interrupt")
		m.sendCancel()
	}
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
