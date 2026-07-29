package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// approvalBarView renders the action bar shown in place of the composer while a
// tool call awaits approval. The pending command itself is shown by the
// approval card in the scrollback, so the bar carries only the choices. It uses
// foreground-only styling (no background card) to avoid background bleed across
// the inner styled segments, and pads to the input bar's footprint so the
// layout does not jump.
func (m *model) approvalBarView() string {
	choices := strings.Join([]string{
		styTool.Render("y") + styMuted.Render(" yes"),
		styTool.Render("n") + styMuted.Render(" no"),
		styTool.Render("esc") + styMuted.Render(" deny"),
	}, "    ")
	line := styAccent.Render("approve?") + "  " + choices
	return lipgloss.NewStyle().Padding(1, 1).Render(line)
}
