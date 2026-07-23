package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/workflow"
)

func TestNewModelKeepsTranscriptMouseWheelStateStable(t *testing.T) {
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

func TestMakeViewDisablesMouseReportingSoTextIsCopyable(t *testing.T) {
	t.Parallel()

	v := makeView("visible output")
	if v.MouseMode != tea.MouseModeNone {
		t.Fatalf("MouseMode = %v, want MouseModeNone so terminal selection/copy works", v.MouseMode)
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

	assertGap("collapsed plan", "Thinking")
	m.workflow = &workflowBlock{title: "Plan", items: []workflow.Item{
		{ID: "1", Text: "Inspect", Status: workflow.Running},
		{ID: "2", Text: "Implement", Status: workflow.Pending},
	}}
	m.workflowVisible = true
	m.layout()
	m.refreshViewport()
	assertGap("expanded plan", "Plan  0/2")
}

func TestViewFitsShortTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		height        int
		workflowItems int
		pickerItems   int
		wantClipped   bool
	}{
		{name: "collapsed", height: 9},
		{name: "expanded", height: 20, workflowItems: 8},
		{name: "long expanded", height: 12, workflowItems: 20, wantClipped: true},
		{name: "long picker", height: 12, pickerItems: 20, wantClipped: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := makeTestModel()
			m.height = tc.height
			m.busy = true
			m.busySince = time.Now()
			m.blocks = []block{toolCallBlock{name: "bash", args: map[string]any{"command": "test"}}}
			if tc.workflowItems > 0 {
				items := make([]workflow.Item, tc.workflowItems)
				for i := range items {
					items[i] = workflow.Item{ID: string(rune('a' + i)), Text: "Step", Status: workflow.Pending}
				}
				m.workflow = &workflowBlock{title: "Plan", items: items}
				m.workflowVisible = true
			}
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
			if tc.workflowItems > 0 && tc.wantClipped && !strings.Contains(plain(view), "more") {
				t.Fatalf("clipped workflow has no visible more marker:\n%s", plain(view))
			}
			if tc.pickerItems > 0 {
				out := plain(view)
				if !strings.Contains(out, "cmdt") || !strings.Contains(out, "(20/20)") {
					t.Fatalf("picker window lost selected item or total count:\n%s", out)
				}
			}
		})
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
