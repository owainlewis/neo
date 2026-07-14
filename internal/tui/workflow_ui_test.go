package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/owainlewis/neo/internal/workflow"
)

func TestWorkflowPanelHeightStaysStableWhenPlanCompletes(t *testing.T) {
	m := makeTestModel()
	m.workflow = &workflowBlock{title: "Checklist", items: []workflow.Item{
		{ID: "1", Text: "inspect", Status: workflow.Done},
		{ID: "2", Text: "test", Status: workflow.Running},
	}}
	m.workflowVisible = true
	m.layout()
	beforePanel := m.workflowPanelHeight()
	beforeViewport := m.viewport.Height()

	m.handleWorkflowEvent(workflow.Event{Action: "complete", ID: "2"})

	if got := m.workflowPanelHeight(); got != beforePanel {
		t.Fatalf("completed panel height = %d, want stable %d", got, beforePanel)
	}
	if got := m.viewport.Height(); got != beforeViewport {
		t.Fatalf("completed viewport height = %d, want stable %d", got, beforeViewport)
	}
}

func TestWorkflowPanelRowsFitTerminalWidth(t *testing.T) {
	const width = 24
	panel := (&workflowBlock{
		title: "A checklist title\nthat is much too long",
		items: []workflow.Item{{
			ID:     "1",
			Text:   "Inspect an extremely long\nand deeply nested component name",
			Status: workflow.Running,
			Detail: "reading /a/very/long/path/to/internal/tui/model.go",
		}},
	}).render(width, nil)

	for i, line := range strings.Split(panel, "\n") {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("panel line %d width = %d, want <= %d: %q", i, got, width, plain(line))
		}
	}
}

func TestWorkflowPanelKeepsProgressAtNarrowWidths(t *testing.T) {
	panel := (&workflowBlock{
		title: "A long checklist title",
		items: []workflow.Item{
			{ID: "1", Text: "inspect", Status: workflow.Done},
			{ID: "2", Text: "test", Status: workflow.Done},
		},
	}).render(8, nil)

	header := strings.Split(plain(panel), "\n")[0]
	if !strings.Contains(header, "2/2") {
		t.Fatalf("narrow header lost progress metadata: %q", header)
	}
	if got := ansi.StringWidth(strings.Split(panel, "\n")[0]); got > 8 {
		t.Fatalf("narrow header width = %d, want <= 8: %q", got, header)
	}
}

func TestWorkflowPanelHasGutterAboveChecklist(t *testing.T) {
	m := makeTestModel()
	m.width = 40
	m.height = 24
	m.workflow = &workflowBlock{title: "Checklist", items: []workflow.Item{{ID: "1", Text: "inspect"}}}
	m.workflowVisible = true
	m.layout()

	lines := make([]string, m.viewport.Height())
	for i := range lines {
		lines[i] = "transcript"
	}
	lines[len(lines)-1] = "TRANSCRIPT-END"
	m.viewport.SetContent(strings.Join(lines, "\n"))
	m.viewport.GotoBottom()

	out := plain(m.View().Content)
	viewLines := strings.Split(out, "\n")
	if got := len(viewLines); got != m.height {
		t.Fatalf("rendered view height = %d, want terminal height %d:\n%s", got, m.height, out)
	}
	transcriptEnd, checklistStart := -1, -1
	for i, line := range viewLines {
		switch strings.TrimSpace(line) {
		case "TRANSCRIPT-END":
			transcriptEnd = i
		case "Checklist  0/1":
			checklistStart = i
		}
	}
	if transcriptEnd < 0 || checklistStart != transcriptEnd+2 || strings.TrimSpace(viewLines[transcriptEnd+1]) != "" {
		t.Fatalf("view missing gutter between transcript and checklist:\n%s", out)
	}
}

func TestBranchMsgUpdatesFooterBranch(t *testing.T) {
	m := makeTestModel()
	m.branch = "main"

	m.Update(branchMsg{branch: "feature/ui-refresh"})

	footer := plain(m.footerLine())
	if !strings.Contains(footer, "feature/ui-refresh") {
		t.Fatalf("footer = %q", footer)
	}
}

func TestWorkflowPanel_TabTogglesVisibility(t *testing.T) {
	m := makeTestModel()
	m.workflow = &workflowBlock{title: "Workflow", items: []workflow.Item{{ID: "1", Text: "first"}}}
	m.workflowVisible = true
	m.layout()

	if got := plain(m.workflowPanelView()); !strings.Contains(got, "Workflow") {
		t.Fatalf("expected workflow panel visible, got %q", got)
	}

	m.Update(keyPress(tea.KeyTab))
	if m.workflowVisible {
		t.Fatal("expected Tab to hide workflow panel")
	}
	if got := m.workflowPanelView(); got != "" {
		t.Fatalf("expected hidden panel to render empty, got %q", got)
	}

	m.Update(keyPress(tea.KeyTab))
	if !m.workflowVisible {
		t.Fatal("expected second Tab to show workflow panel")
	}
}

func TestWorkflowPanel_TabDoesNotStealPickerAcceptance(t *testing.T) {
	withSlashCommands(t, []slashCommand{
		{"/help", "show this list"},
		{"/resume", "resume a session"},
	})
	m := makeTestModel()
	m.workflow = &workflowBlock{title: "Workflow", items: []workflow.Item{{ID: "1", Text: "first"}}}
	m.workflowVisible = true
	m.input.SetValue("/")
	m.updateSlashPicker()
	m.Update(keyPress(tea.KeyDown))

	m.Update(keyPress(tea.KeyTab))

	if got := m.input.Value(); got != "/resume" {
		t.Fatalf("Tab should accept slash picker before toggling workflow, got %q", got)
	}
	if !m.workflowVisible {
		t.Fatal("workflow visibility changed while picker handled Tab")
	}
}

func TestWorkflowPanel_RemainsVisibleWhenCompletedTurnFinishes(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.busySince = time.Now()
	m.turn = turnStats{workflow: true}
	m.workflow = &workflowBlock{title: "Workflow", items: []workflow.Item{
		{ID: "1", Text: "inspect", Status: workflow.Done},
		{ID: "2", Text: "test", Status: workflow.Done},
	}}
	m.workflowVisible = true

	m.Update(sendResultMsg{})

	if m.workflow == nil {
		t.Fatal("completed workflow should remain available for inspection")
	}
	if !m.workflowVisible {
		t.Fatal("completed workflow should remain visible until Tab or the next turn")
	}
}

func TestBusyStatusHeightStaysStableAcrossToolTransitions(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.layout()
	withoutTool := m.viewport.Height()

	m.currentTool = &toolCallBlock{name: "read_file", args: map[string]any{"path": "model.go"}}
	m.layout()
	withTool := m.viewport.Height()

	m.currentTool = nil
	m.layout()
	if got := m.viewport.Height(); got != withoutTool || withTool != withoutTool {
		t.Fatalf("viewport heights without/with/after tool = %d/%d/%d, want stable", withoutTool, withTool, got)
	}
}

func TestLayoutPreservesViewportFollowState(t *testing.T) {
	m := makeTestModel()
	m.layout()
	m.viewport.SetContent(numberedLines(20))
	m.viewport.GotoBottom()
	bottomBefore := m.viewport.YOffset()

	m.busy = true
	m.layout()
	if !m.viewport.AtBottom() || m.viewport.YOffset() <= bottomBefore {
		t.Fatalf("bottom-follow lost after shrink: offset %d -> %d", bottomBefore, m.viewport.YOffset())
	}

	m.viewport.ScrollUp(2)
	scrolledOffset := m.viewport.YOffset()
	m.busy = false
	m.layout()
	if got := m.viewport.YOffset(); got != scrolledOffset {
		t.Fatalf("scrolled offset after resize = %d, want preserved %d", got, scrolledOffset)
	}
}

func TestShortTerminalCompactsWorkflowWithoutOverflow(t *testing.T) {
	m := makeTestModel()
	m.width = 32
	m.height = 20
	m.busy = true
	m.busySince = time.Now()
	m.workflow = &workflowBlock{title: "Checklist", items: []workflow.Item{
		{ID: "1", Text: "one"},
		{ID: "2", Text: "two"},
		{ID: "3", Text: "three"},
		{ID: "4", Text: "four"},
		{ID: "5", Text: "five"},
	}}
	m.workflowVisible = true
	m.layout()
	m.viewport.SetContent("TRANSCRIPT")

	out := plain(m.View().Content)
	if got := len(strings.Split(out, "\n")); got != m.height {
		t.Fatalf("short view height = %d, want %d:\n%s", got, m.height, out)
	}
	if !strings.Contains(out, "Checklist") || !strings.Contains(out, "…") {
		t.Fatalf("short view did not compact checklist:\n%s", out)
	}
}

func TestWorkflowPanel_ClearsCompletedWorkflowBeforeNextTurn(t *testing.T) {
	m := makeTestModel()
	m.workflow = &workflowBlock{title: "Old plan", items: []workflow.Item{
		{ID: "1", Text: "old", Status: workflow.Done},
		{ID: "2", Text: "done", Status: workflow.Skipped},
	}}
	m.workflowVisible = true

	m.submitUserTurn("hello", "hello", nil)

	if m.workflow != nil {
		t.Fatalf("completed workflow should be cleared before next turn, got %+v", m.workflow)
	}
	if m.workflowVisible {
		t.Fatal("cleared workflow should not remain visible")
	}
}
