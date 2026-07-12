package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

func TestMakeViewLeavesMouseAvailableForTerminalSelection(t *testing.T) {
	t.Parallel()

	v := makeView("visible output")
	if v.MouseMode != tea.MouseModeNone {
		t.Fatalf("MouseMode = %v, want MouseModeNone so the terminal can select text", v.MouseMode)
	}
	if !v.AltScreen {
		t.Fatal("AltScreen = false, want true")
	}
}

func TestPageKeysScrollTranscriptWithoutMouseCapture(t *testing.T) {
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
}
