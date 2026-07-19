package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type commandPicker struct {
	visible      bool
	matches      []slashCommand
	selected     int
	dismissedFor string
}

func (m *model) updateSlashPicker() {
	wasVisible := m.picker.visible
	query, ok := slashPickerQuery(m.input.Value())
	if !ok {
		m.hideSlashPicker()
		if wasVisible {
			m.layout()
		}
		return
	}
	if m.picker.dismissedFor != "" {
		if query == m.picker.dismissedFor {
			m.picker.visible = false
			m.picker.matches = nil
			m.picker.selected = 0
			if wasVisible {
				m.layout()
			}
			return
		}
		m.picker.dismissedFor = ""
	}

	var matches []slashCommand
	for _, c := range m.slashCommands() {
		if strings.HasPrefix(c.cmd, query) {
			matches = append(matches, c)
		}
	}
	m.picker.matches = matches
	m.picker.visible = len(matches) > 0
	if m.picker.selected >= len(matches) {
		m.picker.selected = len(matches) - 1
	}
	if m.picker.selected < 0 {
		m.picker.selected = 0
	}
	m.layout()
}

func slashPickerQuery(input string) (string, bool) {
	if !strings.HasPrefix(input, "/") {
		return "", false
	}
	query := strings.TrimSpace(input)
	if strings.ContainsAny(query, " \t\n\r") {
		return "", false
	}
	return query, true
}

func (m *model) moveSlashPickerSelection(delta int) {
	if !m.picker.visible || len(m.picker.matches) == 0 {
		return
	}
	n := len(m.picker.matches)
	m.picker.selected = (m.picker.selected + delta + n) % n
}

func (m *model) acceptSlashPicker(force bool) bool {
	if !m.picker.visible || len(m.picker.matches) == 0 {
		return false
	}
	cmd := m.picker.matches[m.picker.selected].cmd
	current := strings.TrimSpace(m.input.Value())
	if !force && current == cmd {
		return false
	}
	m.input.SetValue(cmd)
	m.picker = commandPicker{dismissedFor: cmd}
	return true
}

func (m *model) dismissSlashPicker() {
	if query, ok := slashPickerQuery(m.input.Value()); ok {
		m.picker.dismissedFor = query
	}
	m.picker.visible = false
	m.picker.matches = nil
	m.picker.selected = 0
}

func (m *model) hideSlashPicker() {
	m.picker.visible = false
	m.picker.matches = nil
	m.picker.selected = 0
	m.picker.dismissedFor = ""
}

func (m *model) slashPickerView() string {
	if !m.picker.visible || len(m.picker.matches) == 0 {
		return ""
	}
	return renderSlashPicker(m.width, m.picker, m.maxInlinePickerRows())
}

func renderSlashPicker(width int, picker commandPicker, rowLimits ...int) string {
	if width <= 0 || len(picker.matches) == 0 {
		return ""
	}
	maxRows := len(picker.matches) + 1
	if len(rowLimits) > 0 {
		maxRows = min(maxRows, rowLimits[0])
	}
	if maxRows < 2 {
		return ""
	}
	start, end := pickerWindow(len(picker.matches), picker.selected, maxRows-1)
	contentWidth := width - 2 // styPicker adds one column of horizontal padding.
	if contentWidth < 1 {
		contentWidth = 1
	}
	cmdWidth := slashPickerCommandWidth(picker.matches)
	maxCmdWidth := contentWidth - 5 // prefix + gap + at least one desc column
	if maxCmdWidth < 1 {
		maxCmdWidth = contentWidth
	}
	if cmdWidth > maxCmdWidth {
		cmdWidth = maxCmdWidth
	}
	descWidth := contentWidth - cmdWidth - 4 // prefix + gap
	if descWidth < 0 {
		descWidth = 0
	}

	var lines []string
	for i := start; i < end; i++ {
		c := picker.matches[i]
		name := padRight(truncate(slashPickerDisplayName(c.cmd), cmdWidth), cmdWidth)
		prefix := "  "
		cmdStyle := styPickerCommand
		descStyle := styMuted
		if i == picker.selected {
			prefix = styPickerSelected.Render("→") + " "
			cmdStyle = styPickerSelected
			descStyle = styPickerSelected
		}
		line := prefix + cmdStyle.Render(name)
		if descWidth > 0 {
			line += "  " + descStyle.Render(truncate(c.desc, descWidth))
		}
		lines = append(lines, line)
	}
	lines = append(lines, styMuted.Render(fmt.Sprintf("(%d/%d)", picker.selected+1, len(picker.matches))))
	return styPicker.Render(strings.Join(lines, "\n"))
}

func pickerWindow(total, selected, limit int) (int, int) {
	if limit <= 0 || total <= 0 {
		return 0, 0
	}
	if limit >= total {
		return 0, total
	}
	selected = min(max(selected, 0), total-1)
	start := max(selected-limit+1, 0)
	end := min(start+limit, total)
	return start, end
}

func slashPickerCommandWidth(commands []slashCommand) int {
	width := 0
	for _, c := range commands {
		if n := ansi.StringWidth(slashPickerDisplayName(c.cmd)); n > width {
			width = n
		}
	}
	if width < 10 {
		return 10
	}
	if width > 24 {
		return 24
	}
	return width
}

func slashPickerDisplayName(cmd string) string {
	return strings.TrimPrefix(cmd, "/")
}
