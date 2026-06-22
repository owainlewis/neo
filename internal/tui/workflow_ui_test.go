package tui

import (
	"strings"
	"testing"

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
