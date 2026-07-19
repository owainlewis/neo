package tui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const filePickerMaxMatches = 8

type filePicker struct {
	root         string
	visible      bool
	files        []string
	matches      []string
	selected     int
	token        filePickerToken
	dismissedFor string
	err          error
}

type filePickerToken struct {
	line  int
	start int
	end   int
	query string
	raw   string
}

func newFilePicker(root string) filePicker {
	return filePicker{root: root}
}

func (m *model) updateFilePicker() {
	wasVisible := m.files.visible
	token, ok := m.currentFilePickerToken()
	if !ok {
		m.hideFilePicker()
		if wasVisible {
			m.layout()
		}
		return
	}
	if m.files.dismissedFor != "" {
		if token.raw == m.files.dismissedFor {
			m.files.visible = false
			m.files.matches = nil
			m.files.selected = 0
			if wasVisible {
				m.layout()
			}
			return
		}
		m.files.dismissedFor = ""
	}

	if err := m.ensureFilePickerFiles(); err != nil {
		m.files.visible = false
		m.files.matches = nil
		m.files.err = err
		if wasVisible {
			m.layout()
		}
		return
	}

	query := strings.ToLower(token.query)
	matches := make([]string, 0, filePickerMaxMatches)
	for _, path := range m.files.files {
		if query == "" || strings.Contains(strings.ToLower(path), query) {
			matches = append(matches, path)
			if len(matches) >= filePickerMaxMatches {
				break
			}
		}
	}
	m.files.token = token
	m.files.matches = matches
	m.files.visible = len(matches) > 0
	if m.files.selected >= len(matches) {
		m.files.selected = len(matches) - 1
	}
	if m.files.selected < 0 {
		m.files.selected = 0
	}
	m.layout()
}

func (m *model) currentFilePickerToken() (filePickerToken, bool) {
	return filePickerTokenAt(m.input.Value(), m.input.Line(), m.input.Column())
}

func filePickerTokenAt(input string, row, col int) (filePickerToken, bool) {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 {
		return filePickerToken{}, false
	}
	if row < 0 || row >= len(lines) {
		row = len(lines) - 1
	}
	line := []rune(lines[row])
	if col < 0 || col > len(line) {
		col = len(line)
	}
	start := col
	for start > 0 && !unicode.IsSpace(line[start-1]) {
		start--
	}
	if start >= col || line[start] != '@' {
		return filePickerToken{}, false
	}
	for i := start + 1; i < col; i++ {
		if line[i] == '@' {
			return filePickerToken{}, false
		}
	}
	query := string(line[start+1 : col])
	if strings.ContainsAny(query, "\t\r\n") {
		return filePickerToken{}, false
	}
	return filePickerToken{
		line:  row,
		start: start,
		end:   col,
		query: query,
		raw:   string(line[start:col]),
	}, true
}

func (m *model) ensureFilePickerFiles() error {
	if len(m.files.files) > 0 || m.files.err != nil {
		return m.files.err
	}
	root := m.files.root
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			m.files.err = err
			return err
		}
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if filePickerSkipDir(d.Name()) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		m.files.err = err
		return err
	}
	sort.Strings(files)
	m.files.files = files
	return nil
}

func filePickerSkipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "dist", "vendor":
		return true
	default:
		return false
	}
}

func (m *model) moveFilePickerSelection(delta int) {
	if !m.files.visible || len(m.files.matches) == 0 {
		return
	}
	n := len(m.files.matches)
	m.files.selected = (m.files.selected + delta + n) % n
}

func (m *model) acceptFilePicker() bool {
	if !m.files.visible || len(m.files.matches) == 0 {
		return false
	}
	path := m.files.matches[m.files.selected]
	replaceFilePickerToken(&m.input, m.files.token, "@"+path)
	m.files = filePicker{
		root:         m.files.root,
		files:        m.files.files,
		dismissedFor: "@" + path,
	}
	return true
}

type valueTextarea interface {
	Value() string
	SetValue(string)
	SetCursorColumn(int)
}

func replaceFilePickerToken(input valueTextarea, token filePickerToken, replacement string) {
	lines := strings.Split(input.Value(), "\n")
	if token.line < 0 || token.line >= len(lines) {
		return
	}
	line := []rune(lines[token.line])
	if token.start < 0 || token.end < token.start || token.end > len(line) {
		return
	}
	nextLine := string(line[:token.start]) + replacement + string(line[token.end:])
	lines[token.line] = nextLine
	input.SetValue(strings.Join(lines, "\n"))
	if token.line == len(lines)-1 {
		input.SetCursorColumn(token.start + len([]rune(replacement)))
	}
}

func (m *model) dismissFilePicker() {
	if token, ok := m.currentFilePickerToken(); ok {
		m.files.dismissedFor = token.raw
	}
	m.files.visible = false
	m.files.matches = nil
	m.files.selected = 0
}

func (m *model) hideFilePicker() {
	m.files.visible = false
	m.files.matches = nil
	m.files.selected = 0
	m.files.dismissedFor = ""
}

func (m *model) filePickerView() string {
	if !m.files.visible || len(m.files.matches) == 0 {
		return ""
	}
	return renderFilePicker(m.width, m.files, m.maxInlinePickerRows())
}

func renderFilePicker(width int, picker filePicker, rowLimits ...int) string {
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
	var lines []string
	for i := start; i < end; i++ {
		path := picker.matches[i]
		prefix := "  "
		style := styPickerCommand
		if i == picker.selected {
			prefix = styPickerSelected.Render("→") + " "
			style = styPickerSelected
		}
		lines = append(lines, prefix+style.Render(truncate(path, max(1, contentWidth-2))))
	}
	lines = append(lines, styMuted.Render(fmt.Sprintf("(%d/%d)", picker.selected+1, len(picker.matches))))
	return styPicker.Render(strings.Join(lines, "\n"))
}
