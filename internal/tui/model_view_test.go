package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/owainlewis/neo/internal/workflow"
)

func TestNewModelEnablesMouseWheel(t *testing.T) {
	t.Parallel()

	base := makeTestModel()
	m, err := newModel(context.Background(), base.ag, "test", "dev", nil, Options{})
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

func TestMakeViewEnablesTranscriptMouseScrolling(t *testing.T) {
	t.Parallel()

	v := makeView("visible output")
	if v.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("MouseMode = %v, want MouseModeCellMotion for wheel events", v.MouseMode)
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

func TestWorkflowToggleKeepsBottomedTranscriptFollowing(t *testing.T) {
	t.Parallel()

	m := makeTestModel()
	m.height = 18
	m.blocks = []block{textBlock{text: numberedLines(24)}}
	m.workflow = &workflowBlock{title: "Plan", items: []workflow.Item{
		{ID: "1", Text: "Inspect", Status: workflow.Running},
		{ID: "2", Text: "Implement", Status: workflow.Pending},
	}}
	m.layout()
	m.refreshViewport()
	if !m.viewport.AtBottom() {
		t.Fatal("viewport should start at the bottom")
	}

	m.Update(keyPress(tea.KeyTab))
	m.appendBlock(textBlock{text: "latest live output"})

	if !m.viewport.AtBottom() {
		t.Fatal("opening the workflow stopped live output following")
	}
	if got := plain(m.viewport.View()); !strings.Contains(got, "latest live output") {
		t.Fatalf("latest output hidden after workflow toggle:\n%s", got)
	}
}

func TestWorkflowTogglePreservesManualScrollPosition(t *testing.T) {
	t.Parallel()

	m := makeTestModel()
	m.height = 18
	m.blocks = []block{textBlock{text: numberedLines(24)}}
	m.workflow = &workflowBlock{title: "Plan", items: []workflow.Item{
		{ID: "1", Text: "Inspect", Status: workflow.Running},
		{ID: "2", Text: "Implement", Status: workflow.Pending},
	}}
	m.layout()
	m.refreshViewport()
	m.viewport.ScrollUp(5)
	before := m.viewport.YOffset()
	if before == 0 || m.viewport.AtBottom() {
		t.Fatalf("test setup did not create a manually scrolled viewport: offset=%d", before)
	}

	m.Update(keyPress(tea.KeyTab))

	if got := m.viewport.YOffset(); got != before {
		t.Fatalf("opening workflow moved manually scrolled transcript to %d, want %d", got, before)
	}

	m.Update(keyPress(tea.KeyTab))

	if got := m.viewport.YOffset(); got != before {
		t.Fatalf("closing workflow moved manually scrolled transcript to %d, want %d", got, before)
	}
}
