package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/permission"
)

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
		styTool.Render("ctrl+o") + styMuted.Render(" preview"),
	}, "    ")
	line := styAccent.Render("approve?") + "  " + choices
	return lipgloss.NewStyle().Padding(1, 1).Render(line)
}
