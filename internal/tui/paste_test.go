package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPasteMsgInsertsIntoInput(t *testing.T) {
	m := makeTestModel()

	m.Update(tea.PasteMsg{Content: "hello\nworld"})

	if got := m.input.Value(); got != "hello\nworld" {
		t.Fatalf("expected pasted content in input, got %q", got)
	}
}

func TestPasteMsgUpdatesInputHeight(t *testing.T) {
	m := makeTestModel()
	before := m.lastInputHeight

	m.Update(tea.PasteMsg{Content: strings.Repeat("line\n", 4)})

	if m.lastInputHeight <= before {
		t.Fatalf("expected paste to grow input height above %d, got %d", before, m.lastInputHeight)
	}
}
