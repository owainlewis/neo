package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type ModelChoice struct {
	ID          string
	Name        string
	Description string
}

type modelBrowser struct {
	visible  bool
	query    string
	selected int
	err      error
}

func normalizeModelChoices(current string, choices []ModelChoice) []ModelChoice {
	var out []ModelChoice
	seen := map[string]bool{}
	for _, choice := range choices {
		choice.ID = strings.TrimSpace(choice.ID)
		if choice.ID == "" || seen[choice.ID] {
			continue
		}
		if strings.TrimSpace(choice.Name) == "" {
			choice.Name = choice.ID
		}
		out = append(out, choice)
		seen[choice.ID] = true
	}
	current = strings.TrimSpace(current)
	if current != "" && !seen[current] {
		out = append([]ModelChoice{{
			ID:          current,
			Name:        current,
			Description: "Current configured model",
		}}, out...)
	}
	return out
}

func (m *model) openModelBrowser() {
	if len(m.modelChoices) == 0 {
		m.appendBlock(noticeBlock{text: "model: " + m.modelTag})
		return
	}
	m.models = modelBrowser{visible: true}
	m.ensureModelSelection()
}

func (m *model) closeModelBrowser() {
	m.models = modelBrowser{}
	m.refreshViewport()
}

func (m *model) handleModelBrowserKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.closeModelBrowser()
	case "up":
		m.moveModelSelection(-1)
	case "down":
		m.moveModelSelection(1)
	case "backspace", "delete":
		if m.models.query != "" {
			m.models.query = dropLastRune(m.models.query)
			m.models.err = nil
			m.ensureModelSelection()
		}
	case "enter":
		m.selectCurrentModel()
	default:
		s := msg.String()
		if utf8.RuneCountInString(s) == 1 && s >= " " && s != "\x7f" {
			m.models.query += s
			m.models.err = nil
			m.ensureModelSelection()
		}
	}
	return m, nil
}

func (m *model) moveModelSelection(delta int) {
	items := m.filteredModels()
	if len(items) == 0 {
		m.models.selected = 0
		return
	}
	n := len(items)
	m.models.selected = (m.models.selected + delta + n) % n
}

func (m *model) ensureModelSelection() {
	items := m.filteredModels()
	if len(items) == 0 {
		m.models.selected = 0
		return
	}
	if m.models.selected >= len(items) {
		m.models.selected = len(items) - 1
	}
	if m.models.selected < 0 {
		m.models.selected = 0
	}
}

func (m *model) selectCurrentModel() {
	items := m.filteredModels()
	if len(items) == 0 {
		return
	}
	choice := items[m.models.selected]
	m.ag.SetModel(choice.ID)
	m.modelTag = choice.ID
	if m.afterSend != nil {
		if err := m.afterSend(); err != nil {
			m.models.err = err
			return
		}
	}
	m.blocks = append(m.blocks, noticeBlock{text: "model: " + choice.ID})
	m.closeModelBrowser()
}

func (m *model) filteredModels() []ModelChoice {
	query := strings.ToLower(strings.TrimSpace(m.models.query))
	out := make([]ModelChoice, 0, len(m.modelChoices))
	for _, choice := range m.modelChoices {
		if query == "" || modelChoiceMatches(choice, query) {
			out = append(out, choice)
		}
	}
	return out
}

func modelChoiceMatches(choice ModelChoice, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		choice.ID,
		choice.Name,
		choice.Description,
	}, "\n"))
	return strings.Contains(haystack, query)
}

func (m *model) modelBrowserView() string {
	items := m.filteredModels()
	header := styAccent.Render("Select a model")
	query := m.models.query
	if query == "" {
		query = styMuted.Render("Type to search")
	} else {
		query = styTool.Render(query)
	}

	listHeight := m.height - 7
	if listHeight < 3 {
		listHeight = 3
	}
	start := 0
	if m.models.selected >= listHeight {
		start = m.models.selected - listHeight + 1
	}
	end := min(len(items), start+listHeight)

	var lines []string
	for i := start; i < end; i++ {
		choice := items[i]
		selected := i == m.models.selected
		prefix := "  "
		style := lipgloss.NewStyle()
		if selected {
			prefix = styTool.Render("›") + " "
			style = lipgloss.NewStyle().Background(colCardBg)
		}
		name := choice.Name
		if choice.ID == m.modelTag {
			name += " " + styMuted.Render("(current)")
		}
		desc := choice.Description
		if desc == "" {
			desc = choice.ID
		}
		line := prefix + styTool.Render(padRight(choice.ID, 22)) + "  " + name
		if desc != "" && desc != choice.ID {
			line += "  " + styMuted.Render(desc)
		}
		lines = append(lines, style.Width(max(1, m.width-2)).Render(truncate(line, max(1, m.width-2))))
	}
	if len(lines) == 0 {
		lines = append(lines, styMuted.Render("No models match."))
	}

	status := fmt.Sprintf("%d / %d", min(m.models.selected+1, len(items)), len(items))
	if len(items) == 0 {
		status = "0 / 0"
	}
	footer := strings.Join([]string{
		styTool.Render("enter") + styMuted.Render(" select"),
		styTool.Render("esc") + styMuted.Render(" exit"),
		styTool.Render("↑/↓") + styMuted.Render(" browse"),
	}, "    ")

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(header)
	sb.WriteString("\n\n")
	sb.WriteString(query)
	sb.WriteString("\n\n")
	sb.WriteString(strings.Join(lines, "\n"))
	if m.models.err != nil {
		sb.WriteString("\n\n")
		sb.WriteString(styErr.Render("! " + m.models.err.Error()))
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
