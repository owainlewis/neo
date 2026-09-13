package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestNewModelKeepsTranscriptMouseWheelStateStable(t *testing.T) {
	t.Parallel()

	base := makeTestModel()
	m, err := newModel(context.Background(), base.ag, "test", "dev", "", nil, Options{})
	if err != nil {
		t.Fatalf("new model: %v", err)
	}
	if !m.viewport.MouseWheelEnabled {
		t.Fatal("MouseWheelEnabled = false, want transcript wheel scrolling")
	}
	m.viewport.SetWidth(10)
	m.viewport.SetContent(strings.Repeat("x", 40))
	for _, msg := range []tea.MouseWheelMsg{
		tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelRight}),
		tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown, Mod: tea.ModShift}),
	} {
		m.Update(msg)
		if got := m.viewport.XOffset(); got != 0 {
			t.Fatalf("horizontal wheel changed X offset to %d, want 0", got)
		}
	}
}

func TestNewModelFilePickerUsesWorkingDirectory(t *testing.T) {
	ancestor := t.TempDir()
	if err := os.Mkdir(filepath.Join(ancestor, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	workingDir := filepath.Join(ancestor, "nested", "project")
	file := filepath.Join(workingDir, "docs", "file.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	base := makeTestModel()
	m, err := newModel(context.Background(), base.ag, "test", "dev", workingDir, nil, Options{})
	if err != nil {
		t.Fatalf("new model: %v", err)
	}
	m.input.SetValue("@docs/file")
	m.updateFilePicker()

	if m.files.root != workingDir {
		t.Fatalf("file picker root = %q, want working directory %q", m.files.root, workingDir)
	}
	if len(m.files.matches) != 1 || m.files.matches[0] != "docs/file.md" {
		t.Fatalf("file picker matches = %v, want [docs/file.md]", m.files.matches)
	}
}

// The alt screen hides the terminal's own scrollback, so wheel events are the
// only way most users reach transcript history. Mouse reporting must stay on;
// terminals keep drag selection available behind shift.
func TestMakeViewEnablesMouseReportingSoTheWheelScrolls(t *testing.T) {
	t.Parallel()

	v := makeView("visible output")
	if v.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("MouseMode = %v, want MouseModeCellMotion so the wheel scrolls the transcript", v.MouseMode)
	}
	if !v.AltScreen {
		t.Fatal("AltScreen = false, want true")
	}
}

func TestPageKeysAndMouseWheelScrollTranscript(t *testing.T) {
	t.Parallel()

	v := viewport.New(viewport.WithWidth(20), viewport.WithHeight(3))
	v.SetContent(strings.Join([]string{"one", "two", "three", "four", "five", "six"}, "\n"))
	v.GotoBottom()
	m := model{viewport: v}

	before := m.viewport.YOffset()
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	if got := m.viewport.YOffset(); got >= before {
		t.Fatalf("page up offset = %d, want less than %d", got, before)
	}

	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	if got := m.viewport.YOffset(); got != before {
		t.Fatalf("page down offset = %d, want %d", got, before)
	}

	m.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	if got := m.viewport.YOffset(); got >= before {
		t.Fatalf("wheel up offset = %d, want less than %d", got, before)
	}
	m.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	if got := m.viewport.YOffset(); got != before {
		t.Fatalf("wheel down offset = %d, want %d", got, before)
	}

	m.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp, Mod: tea.ModShift}))
	if got := m.viewport.YOffset(); got >= before {
		t.Fatalf("shift+wheel up offset = %d, want less than %d", got, before)
	}
	m.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown, Mod: tea.ModShift}))
	if got := m.viewport.YOffset(); got != before {
		t.Fatalf("shift+wheel down offset = %d, want %d", got, before)
	}
}

func TestShiftArrowsScrollTranscriptOneLine(t *testing.T) {
	t.Parallel()

	v := viewport.New(viewport.WithWidth(20), viewport.WithHeight(3))
	v.SetContent(strings.Join([]string{"one", "two", "three", "four", "five", "six"}, "\n"))
	v.GotoBottom()
	m := model{viewport: v}

	before := m.viewport.YOffset()
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp, Mod: tea.ModShift}))
	if got := m.viewport.YOffset(); got != before-1 {
		t.Fatalf("shift+up offset = %d, want %d", got, before-1)
	}

	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown, Mod: tea.ModShift}))
	if got := m.viewport.YOffset(); got != before {
		t.Fatalf("shift+down offset = %d, want %d", got, before)
	}
}

func TestPageKeysScrollTranscriptDuringApproval(t *testing.T) {
	t.Parallel()

	v := viewport.New(viewport.WithWidth(20), viewport.WithHeight(3))
	v.SetContent(strings.Join([]string{"one", "two", "three", "four", "five", "six"}, "\n"))
	v.GotoBottom()
	m := model{viewport: v, approval: &approvalState{}}

	before := m.viewport.YOffset()
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	if got := m.viewport.YOffset(); got >= before {
		t.Fatalf("page up offset during approval = %d, want less than %d", got, before)
	}
	if m.approval == nil {
		t.Fatal("page up dismissed approval")
	}

	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	if got := m.viewport.YOffset(); got != before {
		t.Fatalf("page down offset during approval = %d, want %d", got, before)
	}
	if m.approval == nil {
		t.Fatal("page down dismissed approval")
	}
}

func TestNewTranscriptActivityDoesNotSnapScrolledViewportToBottom(t *testing.T) {
	t.Parallel()

	m := makeTestModel()
	m.viewport.SetHeight(3)
	m.blocks = []block{textBlock{text: strings.Join([]string{
		"one", "two", "three", "four", "five", "six",
	}, "\n")}}
	m.refreshViewport()
	m.viewport.ScrollUp(2)
	before := m.viewport.YOffset()

	m.blocks = append(m.blocks, textBlock{text: "seven"})
	m.refreshViewport()

	if got := m.viewport.YOffset(); got != before {
		t.Fatalf("offset after new activity = %d, want preserved %d", got, before)
	}
}

func TestNewTranscriptActivityFollowsWhenViewportIsAtBottom(t *testing.T) {
	t.Parallel()

	m := makeTestModel()
	m.viewport.SetHeight(3)
	m.blocks = []block{textBlock{text: strings.Join([]string{
		"one", "two", "three", "four", "five", "six",
	}, "\n")}}
	m.refreshViewport()
	before := m.viewport.YOffset()

	m.blocks = append(m.blocks, textBlock{text: "seven"})
	m.refreshViewport()

	if got := m.viewport.YOffset(); got <= before || !m.viewport.AtBottom() {
		t.Fatalf("offset after new activity = %d, want new bottom below %d", got, before)
	}
}

func TestViewSeparatesTranscriptFromProgress(t *testing.T) {
	t.Parallel()

	m := makeTestModel()
	m.height = 18
	m.busy = true
	m.busySince = time.Now()
	for i := 0; i < 24; i++ {
		m.blocks = append(m.blocks, toolCallBlock{name: "bash", args: map[string]any{"command": "cmd" + string(rune('a'+i))}})
	}
	m.layout()
	m.refreshViewport()

	assertGap := func(label, progressText string) {
		t.Helper()
		lines := strings.Split(plain(m.View().Content), "\n")
		outputLine := -1
		progressLine := -1
		for i, line := range lines {
			if strings.Contains(line, "Ran cmdx") {
				outputLine = i
			}
			if strings.Contains(line, progressText) {
				progressLine = i
			}
		}
		if outputLine < 0 || progressLine < 0 {
			t.Fatalf("%s: missing output or progress row:\n%s", label, strings.Join(lines, "\n"))
		}
		if progressLine-outputLine < 3 {
			t.Fatalf("%s: output row %d and progress row %d need a full breathing row:\n%s", label, outputLine, progressLine, strings.Join(lines, "\n"))
		}
	}

	assertGap("busy", "Understanding request")
}

func TestViewFitsShortTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		height      int
		pickerItems int
		wantClipped bool
	}{
		{name: "short", height: 9},
		{name: "long picker", height: 12, pickerItems: 20, wantClipped: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := makeTestModel()
			m.height = tc.height
			m.busy = true
			m.busySince = time.Now()
			m.blocks = []block{toolCallBlock{name: "bash", args: map[string]any{"command": "test"}}}
			if tc.pickerItems > 0 {
				m.picker.matches = make([]slashCommand, tc.pickerItems)
				for i := range m.picker.matches {
					m.picker.matches[i] = slashCommand{cmd: "/cmd" + string(rune('a'+i)), desc: "Command"}
				}
				m.picker.visible = true
				m.picker.selected = tc.pickerItems - 1
			}
			m.layout()
			m.refreshViewport()

			view := m.View().Content
			if got := lipgloss.Height(view); got > m.height {
				t.Fatalf("rendered height = %d, want <= terminal height %d:\n%s", got, m.height, plain(view))
			}
			if tc.pickerItems > 0 {
				out := plain(view)
				if !strings.Contains(out, "cmdt") || !strings.Contains(out, "20/20") {
					t.Fatalf("picker window lost selected item or total count:\n%s", out)
				}
			}
		})
	}
}
