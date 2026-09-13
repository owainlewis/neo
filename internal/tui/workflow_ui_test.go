package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/skills"
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

func TestFooterStaysOnOneLineAndSuppressesMissingGit(t *testing.T) {
	m := makeTestModel()
	m.width = 28
	m.cwd = "~/Code/a-long-repository"
	m.branch = "no-git"
	m.providerTag = "anthropic"
	m.modelTag = "claude-sonnet"

	footer := m.footerLine()
	if strings.Contains(plain(footer), "no-git") {
		t.Fatalf("footer exposed no-git sentinel: %q", plain(footer))
	}
	if got := lipgloss.Width(footer); got > m.width {
		t.Fatalf("footer width = %d, want <= %d: %q", got, m.width, plain(footer))
	}
}

func TestIdleStatusStaysMinimal(t *testing.T) {
	m := makeTestModel()
	m.width = 80
	out := plain(m.statusLine())
	if out != " ● ready" {
		t.Fatalf("idle status = %q", out)
	}
}

func TestWorkflowProgressDistinguishesFailedPlan(t *testing.T) {
	m := makeTestModel()
	m.workflow = &workflowBlock{items: []workflow.Item{
		{ID: "1", Text: "Implement", Status: workflow.Done},
		{ID: "2", Text: "Verify", Status: workflow.Failed},
	}}
	if got := m.workflowProgress(); got != "2/2 Plan finished · 1 failed" {
		t.Fatalf("workflow progress = %q", got)
	}
}

func TestWorkflowCreateAppendsChecklistBlockAndReportsProgress(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.busySince = time.Now()
	m.handleWorkflowEvent(workflow.Event{
		Action: "create",
		State: workflow.State{Title: "Code change", Items: []workflow.Item{
			{ID: "1", Text: "Inspect", Status: workflow.Pending},
			{ID: "2", Text: "Implement", Status: workflow.Pending},
		}},
	})
	m.handleWorkflowEvent(workflow.Event{Action: "start", ID: "1"})

	if len(m.blocks) != 1 {
		t.Fatalf("blocks = %d, want the checklist appended to the transcript", len(m.blocks))
	}
	wb, ok := m.blocks[0].(*workflowBlock)
	if !ok || wb != m.workflow {
		t.Fatalf("block = %T, want the live workflow block", m.blocks[0])
	}
	if got := renderPlain(wb, 80); !strings.Contains(got, "Code change") || !strings.Contains(got, "● Inspect") {
		t.Fatalf("checklist render = %q", got)
	}
	if got := plain(m.statusLine()); !strings.Contains(got, "1/2 Inspect") || strings.Contains(got, "tab") {
		t.Fatalf("status should carry compact workflow progress and no panel hint: %q", got)
	}

	// Updates mutate the same block in place rather than appending.
	m.handleWorkflowEvent(workflow.Event{Action: "complete", ID: "1"})
	if len(m.blocks) != 1 || !strings.Contains(renderPlain(wb, 80), "✓ Inspect") {
		t.Fatalf("update did not mutate the checklist in place: %d blocks, %q", len(m.blocks), renderPlain(wb, 80))
	}
}

func TestSkillLabelStaysAheadOfWorkflowProgress(t *testing.T) {
	m := makeTestModel()
	m.skills = skills.Defaults()
	cmd := m.handleSlashCommand("/review")
	if cmd == nil {
		t.Fatal("expected review skill to start")
	}
	m.handleWorkflowEvent(workflow.Event{
		Generation: m.conversationGeneration,
		Action:     "create",
		State: workflow.State{Title: "Review current change", Items: []workflow.Item{
			{ID: "1", Text: "Inspect full diff", Status: workflow.Pending},
			{ID: "2", Text: "Run checks", Status: workflow.Pending},
		}},
	})
	m.handleWorkflowEvent(workflow.Event{Generation: m.conversationGeneration, Action: "start", ID: "1"})

	got := plain(m.statusLine())
	if !strings.Contains(got, "Review · 1/2 Inspect full diff") {
		t.Fatalf("phase and workflow status = %q", got)
	}

	m.approval = &approvalState{}
	if got := plain(m.statusLine()); !strings.Contains(got, "Review · Waiting for approval") {
		t.Fatalf("approval status lost phase: %q", got)
	}
}

func TestFailedSkillWorkflowKeepsLabelInCompletionReceipt(t *testing.T) {
	m := makeTestModel()
	m.turn = turnStats{label: "Review", workflow: true}
	m.workflow = &workflowBlock{items: []workflow.Item{
		{ID: "1", Text: "Inspect", Status: workflow.Done},
		{ID: "2", Text: "Verify", Status: workflow.Failed},
	}}

	summary, ok := m.resultSummary(nil, time.Second)
	if !ok {
		t.Fatal("expected result summary")
	}
	if got := plain(summary.render(80, nil)); !strings.Contains(got, "Review finished with issues") {
		t.Fatalf("failed phase receipt = %q", got)
	}
}

// A new turn detaches the previous checklist even if it was left running
// (for example after Esc), so stale items never absorb the next turn's activity.
// The old block stays in the transcript as history.
func TestNewTurnDetachesPreviousWorkflow(t *testing.T) {
	m := makeTestModel()
	old := &workflowBlock{title: "Old plan", items: []workflow.Item{
		{ID: "1", Text: "old", Status: workflow.Done},
		{ID: "2", Text: "still running", Status: workflow.Running},
	}}
	m.workflow = old
	m.blocks = append(m.blocks, old)

	m.submitUserTurn("hello", "hello", nil)
	t.Cleanup(func() {
		if m.sendCancel != nil {
			m.sendCancel()
		}
	})

	if m.workflow != nil {
		t.Fatalf("previous workflow should be detached before the next turn, got %+v", m.workflow)
	}
	if m.blocks[0] != block(old) {
		t.Fatal("previous checklist should remain in the transcript")
	}
	m.handleEvent(agent.Event{Kind: agent.EventToolCall, Name: "read_file", Args: map[string]any{"path": "a.go"}})
	if old.items[1].Detail != "" {
		t.Fatalf("stale running item absorbed new activity: %q", old.items[1].Detail)
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
	m.handleEvent(agent.Event{Kind: agent.EventToolCall, Name: "read_file", Args: map[string]any{"path": "AGENTS.md"}})

	if m.workflow == nil || len(m.workflow.items) != 2 {
		t.Fatalf("workflow = %+v, want two items", m.workflow)
	}
	if got := m.workflow.items[0].Text; got != "Inspect the issue" {
		t.Fatalf("first step = %q, want preserved text", got)
	}
	if got := m.workflow.items[1].Text; got != "Implement the change" {
		t.Fatalf("second step = %q, want preserved text", got)
	}
	if got := m.workflow.items[0].Detail; got != "Reading AGENTS.md" {
		t.Fatalf("active step detail = %q, want attached activity", got)
	}
}

// A workflow event produced by the previous turn but delivered after the next
// one starts must not become the live checklist.
func TestLateWorkflowEventFromPreviousTurnIsIgnored(t *testing.T) {
	m := makeTestModel()
	previous := m.conversationGeneration
	m.submitUserTurn("next", "next", nil)
	t.Cleanup(func() {
		if m.sendCancel != nil {
			m.sendCancel()
		}
	})

	m.handleWorkflowEvent(workflow.Event{Generation: previous, Action: "create",
		State: workflow.State{Title: "Old", Items: []workflow.Item{{ID: "1", Text: "old"}}}})

	if m.workflow != nil {
		t.Fatalf("late event from the previous turn became the live workflow: %+v", m.workflow)
	}
}
