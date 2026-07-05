package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/session"
)

type sessionBrowser struct {
	visible   bool
	items     []session.Metadata
	query     string
	selected  int
	filterCWD bool
	err       error
}

func (m *model) openSessionBrowser() {
	if m.sessionStore == nil {
		m.appendBlock(errorBlock{err: fmt.Errorf("sessions are unavailable in this view")})
		return
	}
	items, err := m.sessionStore.List(m.ctx)
	if err != nil {
		m.appendBlock(errorBlock{err: err})
		return
	}
	m.sessions = sessionBrowser{
		visible:   true,
		items:     items,
		filterCWD: true,
	}
	m.ensureSessionSelection()
}

func (m *model) closeSessionBrowser() {
	m.sessions = sessionBrowser{}
	m.refreshViewport()
}

func (m *model) handleSessionBrowserKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.closeSessionBrowser()
	case "up":
		m.moveSessionSelection(-1)
	case "down":
		m.moveSessionSelection(1)
	case "tab", "left", "right":
		m.sessions.filterCWD = !m.sessions.filterCWD
		m.sessions.err = nil
		m.ensureSessionSelection()
	case "backspace", "delete":
		if m.sessions.query != "" {
			m.sessions.query = dropLastRune(m.sessions.query)
			m.sessions.err = nil
			m.ensureSessionSelection()
		}
	case "enter":
		m.resumeSelectedSession()
	default:
		s := msg.String()
		if utf8.RuneCountInString(s) == 1 && s >= " " && s != "\x7f" {
			m.sessions.query += s
			m.sessions.err = nil
			m.ensureSessionSelection()
		}
	}
	return m, nil
}

func (m *model) moveSessionSelection(delta int) {
	items := m.filteredSessions()
	if len(items) == 0 {
		m.sessions.selected = 0
		return
	}
	n := len(items)
	m.sessions.selected = (m.sessions.selected + delta + n) % n
}

func (m *model) ensureSessionSelection() {
	items := m.filteredSessions()
	if len(items) == 0 {
		m.sessions.selected = 0
		return
	}
	if m.sessions.selected >= len(items) {
		m.sessions.selected = len(items) - 1
	}
	if m.sessions.selected < 0 {
		m.sessions.selected = 0
	}
}

func (m *model) resumeSelectedSession() {
	items := m.filteredSessions()
	if len(items) == 0 {
		return
	}
	meta := items[m.sessions.selected]
	if !m.sessionInCurrentCWD(meta) {
		m.sessions.err = fmt.Errorf("session cwd is %s; use `neo resume %s` from a shell", shortSessionPath(meta.CWD), meta.ID)
		return
	}
	sess, err := m.sessionStore.Load(m.ctx, meta.ID)
	if err != nil {
		m.sessions.err = err
		return
	}
	m.ag.ReplaceTranscript(sess.Messages)
	m.ag.SetUsage(sess.Usage)
	m.currentSessionID = sess.Metadata.ID
	if sess.Metadata.CWD != "" {
		m.currentSessionCWD = sess.Metadata.CWD
	}
	if m.onSessionResume != nil {
		m.onSessionResume(sess)
	}

	var next []block
	if len(m.blocks) > 0 {
		if splash, ok := m.blocks[0].(splashBlock); ok {
			next = append(next, splash)
		}
	}
	m.blocks = append(next, noticeBlock{text: "resumed session: " + sessionTitle(sess.Metadata)})
	m.appendTranscript(sess.Messages)
	m.closeSessionBrowser()
}

func (m *model) filteredSessions() []session.Metadata {
	query := strings.ToLower(strings.TrimSpace(m.sessions.query))
	out := make([]session.Metadata, 0, len(m.sessions.items))
	for _, item := range m.sessions.items {
		if m.sessions.filterCWD && !m.sessionInCurrentCWD(item) {
			continue
		}
		if query != "" && !sessionMatches(item, query) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (m *model) sessionInCurrentCWD(meta session.Metadata) bool {
	if meta.CWD == "" || m.currentSessionCWD == "" {
		return true
	}
	a, errA := filepath.Abs(meta.CWD)
	b, errB := filepath.Abs(m.currentSessionCWD)
	if errA != nil || errB != nil {
		return filepath.Clean(meta.CWD) == filepath.Clean(m.currentSessionCWD)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func sessionMatches(meta session.Metadata, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		meta.ID,
		meta.Title,
		meta.CWD,
		meta.Model,
		meta.Source,
	}, "\n"))
	return strings.Contains(haystack, query)
}

func (m *model) sessionBrowserView() string {
	items := m.filteredSessions()
	header := styAccent.Render("Resume a previous session")
	query := m.sessions.query
	if query == "" {
		query = styMuted.Render("Type to search")
	} else {
		query = styTool.Render(query)
	}
	filter := "Filter: "
	if m.sessions.filterCWD {
		filter += styTool.Render("[cwd]") + styMuted.Render(" All")
	} else {
		filter += styMuted.Render("cwd ") + styTool.Render("[All]")
	}

	listHeight := m.height - 8
	if listHeight < 3 {
		listHeight = 3
	}
	start := 0
	if m.sessions.selected >= listHeight {
		start = m.sessions.selected - listHeight + 1
	}
	end := min(len(items), start+listHeight)

	var lines []string
	for i := start; i < end; i++ {
		item := items[i]
		selected := i == m.sessions.selected
		prefix := "  "
		style := lipgloss.NewStyle()
		if selected {
			prefix = styTool.Render("›") + " "
			style = lipgloss.NewStyle().Background(colCardBg)
		}
		age := padRight(relativeTime(item.UpdatedAt), 8)
		title := sessionTitle(item)
		if item.ID == m.currentSessionID {
			title += " " + styMuted.Render("(current)")
		}
		if !m.sessionInCurrentCWD(item) {
			title += " " + styMuted.Render(shortSessionPath(item.CWD))
		}
		line := prefix + styMuted.Render(age) + "  " + title
		lines = append(lines, style.Width(max(1, m.width-2)).Render(truncate(line, max(1, m.width-2))))
	}
	if len(lines) == 0 {
		lines = append(lines, styMuted.Render("No sessions match."))
	}

	status := fmt.Sprintf("%d / %d", min(m.sessions.selected+1, len(items)), len(items))
	if len(items) == 0 {
		status = "0 / 0"
	}
	footer := strings.Join([]string{
		styTool.Render("enter") + styMuted.Render(" resume"),
		styTool.Render("esc") + styMuted.Render(" exit"),
		styTool.Render("tab") + styMuted.Render(" filter"),
		styTool.Render("↑/↓") + styMuted.Render(" browse"),
	}, "    ")

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(header)
	sb.WriteString("\n\n")
	sb.WriteString(query)
	if m.width > 50 {
		gap := m.width - lipgloss.Width(query) - lipgloss.Width(filter) - 2
		if gap < 1 {
			gap = 1
		}
		sb.WriteString(strings.Repeat(" ", gap))
		sb.WriteString(filter)
	}
	sb.WriteString("\n\n")
	sb.WriteString(strings.Join(lines, "\n"))
	if m.sessions.err != nil {
		sb.WriteString("\n\n")
		sb.WriteString(styErr.Render("! " + m.sessions.err.Error()))
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

func sessionTitle(meta session.Metadata) string {
	if strings.TrimSpace(meta.Title) != "" {
		return meta.Title
	}
	return "(untitled)"
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	if d < time.Minute {
		return "now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	if d < 30*24*time.Hour {
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
	return t.Local().Format("2006-01-02")
}

func shortSessionPath(path string) string {
	if path == "" {
		return "-"
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" && (path == home || strings.HasPrefix(path, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func dropLastRune(s string) string {
	if s == "" {
		return s
	}
	_, size := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-size]
}
