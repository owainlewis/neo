package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/permission"
)

type permissionChoice struct {
	mode        string
	description string
}

type permissionPicker struct {
	visible  bool
	selected int
	err      error
}

var permissionChoices = []permissionChoice{
	{mode: "ask", description: "Read/search tools run automatically; bash and file mutations ask first."},
	{mode: "trusted", description: "Built-in tools run automatically; high-risk bash commands ask first; workspace path checks still apply."},
	{mode: "readonly", description: "Read/search tools run automatically; bash and file mutations are denied."},
}

func (m *model) openPermissionPicker() {
	m.perms = permissionPicker{visible: true}
	for i, choice := range permissionChoices {
		if choice.mode == m.currentPermissionMode() {
			m.perms.selected = i
			break
		}
	}
}

func (m *model) closePermissionPicker() {
	m.perms = permissionPicker{}
	m.refreshViewport()
}

func (m *model) handlePermissionPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.closePermissionPicker()
	case "up":
		m.movePermissionSelection(-1)
	case "down":
		m.movePermissionSelection(1)
	case "enter":
		m.selectCurrentPermission()
	}
	return m, nil
}

func (m *model) movePermissionSelection(delta int) {
	n := len(permissionChoices)
	m.perms.selected = (m.perms.selected + delta + n) % n
}

func (m *model) selectCurrentPermission() {
	if len(permissionChoices) == 0 {
		return
	}
	choice := permissionChoices[m.perms.selected]
	if err := m.ag.SetPermissionMode(choice.mode); err != nil {
		m.perms.err = err
		return
	}
	m.permissionMode = choice.mode
	m.blocks = append(m.blocks, noticeBlock{text: "permissions: " + choice.mode})
	m.closePermissionPicker()
}

// approvalBarView renders the action bar shown in place of the composer while a
// tool call awaits approval. The pending command itself is shown by the
// approval card in the scrollback, so the bar carries only the choices. It uses
// foreground-only styling (no background card) to avoid background bleed across
// the inner styled segments, and pads to the input bar's footprint so the
// layout does not jump.
func (m *model) approvalBarView() string {
	req := m.approval.req
	label := permission.RuleFor(permission.Request{ToolName: req.ToolName, Args: req.Args}).Label()

	choices := strings.Join([]string{
		styTool.Render("y") + styMuted.Render(" yes"),
		styTool.Render("a") + styMuted.Render(" always allow "+label),
		styTool.Render("n") + styMuted.Render(" no"),
		styTool.Render("esc") + styMuted.Render(" deny"),
	}, "    ")
	line := styAccent.Render("approve?") + "  " + choices
	return lipgloss.NewStyle().Padding(1, 1).Render(line)
}

func (m *model) currentPermissionMode() string {
	mode := strings.TrimSpace(m.permissionMode)
	if mode == "" {
		return "ask"
	}
	return mode
}

func (m *model) permissionPickerView() string {
	header := styAccent.Render("Select permission mode")
	current := "Current: " + styTool.Render(m.currentPermissionMode())

	var lines []string
	for i, choice := range permissionChoices {
		selected := i == m.perms.selected
		prefix := "  "
		style := lipgloss.NewStyle()
		if selected {
			prefix = styTool.Render("›") + " "
			style = lipgloss.NewStyle().Background(colCardBg)
		}
		mode := choice.mode
		if choice.mode == m.currentPermissionMode() {
			mode += " " + styMuted.Render("(current)")
		}
		line := prefix + styTool.Render(mode)
		lines = append(lines, style.Width(max(1, m.width-2)).Render(line))

		descWidth := max(1, m.width-6)
		desc := strings.ReplaceAll(wrap(choice.description, descWidth), "\n", "\n    ")
		lines = append(lines, "    "+styMuted.Render(desc))
	}

	status := fmt.Sprintf("%d / %d", min(m.perms.selected+1, len(permissionChoices)), len(permissionChoices))
	footer := strings.Join([]string{
		styTool.Render("enter") + styMuted.Render(" select"),
		styTool.Render("esc") + styMuted.Render(" exit"),
		styTool.Render("↑/↓") + styMuted.Render(" browse"),
	}, "    ")

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(header)
	sb.WriteString("\n\n")
	sb.WriteString(current)
	sb.WriteString("\n\n")
	sb.WriteString(strings.Join(lines, "\n"))
	if m.perms.err != nil {
		sb.WriteString("\n\n")
		sb.WriteString(styErr.Render("! " + m.perms.err.Error()))
	}
	used := strings.Count(sb.String(), "\n")
	for used < m.height-3 {
		sb.WriteString("\n")
		used++
	}
	sb.WriteString(styDim.Render(strings.Repeat("─", max(1, m.width-1))))
	sb.WriteString("\n")
	sb.WriteString(footer)
	if m.width > lipgloss.Width(footer)+lipgloss.Width(status)+2 {
		sb.WriteString(strings.Repeat(" ", m.width-lipgloss.Width(footer)-lipgloss.Width(status)-2))
		sb.WriteString(styMuted.Render(status))
	}
	return sb.String()
}
