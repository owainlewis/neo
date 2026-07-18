package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/owainlewis/neo/internal/workflow"
)

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

func TestWorkflowPanel_HidesWhenCompletedTurnFinishes(t *testing.T) {
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
	if m.workflowVisible {
		t.Fatal("completed workflow should hide after the turn finishes")
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

func TestUserWorkflowWaitsForWorkflowTool(t *testing.T) {
	m := makeTestModel()
	input := "Follow this workflow:\n1. Inspect the issue\n2. Implement the change"

	m.submitUserTurn(input, input, nil)

	if m.workflow != nil {
		t.Fatalf("user text should not bypass the workflow tool, got %+v", m.workflow)
	}
}

func TestWorkflowToolPreservesStepsAndAttachesActivity(t *testing.T) {
	m := makeTestModel()
	m.handleWorkflowEvent(workflow.Event{
		Action: "create",
		State: workflow.State{
			Title: "Code change",
			Items: []workflow.Item{
				{ID: "1", Text: "Inspect the issue", Status: workflow.Pending},
				{ID: "2", Text: "Implement the change", Status: workflow.Pending},
			},
		},
	})
	m.handleWorkflowEvent(workflow.Event{Action: "start", ID: "1"})
	m.noteWorkflowActivity("read_file AGENTS.md")

	if m.workflow == nil || len(m.workflow.items) != 2 {
		t.Fatalf("workflow = %+v, want two items", m.workflow)
	}
	if got := m.workflow.items[0].Text; got != "Inspect the issue" {
		t.Fatalf("first step = %q, want preserved text", got)
	}
	if got := m.workflow.items[1].Text; got != "Implement the change" {
		t.Fatalf("second step = %q, want preserved text", got)
	}
	if got := m.workflow.items[0].Detail; got != "read_file AGENTS.md" {
		t.Fatalf("active step detail = %q, want attached activity", got)
	}
}
