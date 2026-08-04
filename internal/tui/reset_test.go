package tui

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/factory"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/phase"
	"github.com/owainlewis/neo/internal/skills"
	"github.com/owainlewis/neo/internal/tools"
	"github.com/owainlewis/neo/internal/workflow"
)

func TestResetConversationClearsConversationState(t *testing.T) {
	m := makeTestModel()
	m.ag = agent.New(agent.Config{
		Model:    "selected-model",
		Provider: &llmtest.FakeProvider{},
		Tools:    tools.NewRegistry(tools.ReadFile{}),
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: []llm.ContentBlock{{Type: "text", Text: "old conversation"}},
		}},
		Usage: llm.Usage{InputTokens: 11, OutputTokens: 7},
	})
	m.blocks = []block{noticeBlock{text: "old transcript"}}
	m.busy = true
	m.busySince = time.Now()
	m.currentTool = &toolCallBlock{name: "read_file"}
	group := &parallelBlock{id: "group"}
	row := &parallelCallRow{id: "call", groupID: "group"}
	m.parallelGroups = map[string]*parallelBlock{"group": group}
	m.parallelCalls = map[string]*parallelCallRow{"call": row}
	m.workflow = &workflowBlock{
		title: "Old workflow",
		items: []workflow.Item{{ID: "step", Text: "Old step", Status: workflow.Running}},
	}
	m.workflowVisible = true
	m.turn = turnStats{tools: 2, errors: 1, workflow: true, direct: true, phase: "Review"}
	tree := newTreeBlock()
	m.activeTree = tree
	m.treeIndex = map[int]*treeBlock{1: tree}
	approvalReply := make(chan bool, 1)
	m.approval = &approvalState{req: agent.ApprovalRequest{ToolName: "bash"}, reply: approvalReply}
	canceled := false
	m.sendCancel = func() { canceled = true }
	m.pendingSteering = []string{"old steering"}
	m.queued = &queuedTurn{displayText: "old follow-up", agentText: "old follow-up"}
	m.input.SetValue("old draft")
	m.picker = commandPicker{visible: true, dismissedFor: "/old"}
	m.files.visible = true
	m.files.matches = []string{"old.go"}
	m.files.selected = 1
	m.files.token = filePickerToken{raw: "@old"}
	m.models = modelBrowser{visible: true, query: "old"}

	m.resetConversation()

	if got := m.ag.Transcript(); len(got) != 0 {
		t.Fatalf("transcript = %#v, want empty", got)
	}
	if got := m.ag.Usage(); got != (llm.Usage{}) {
		t.Fatalf("usage = %+v, want zero", got)
	}
	if len(m.blocks) != 0 || m.viewport.TotalLineCount() != 0 {
		t.Fatalf("rendered transcript was not cleared: blocks=%d lines=%d", len(m.blocks), m.viewport.TotalLineCount())
	}
	if m.busy || !m.busySince.IsZero() || m.currentTool != nil {
		t.Fatalf("active turn state remains: busy=%v since=%v tool=%#v", m.busy, m.busySince, m.currentTool)
	}
	if m.parallelGroups != nil || m.parallelCalls != nil {
		t.Fatalf("parallel state remains: groups=%#v calls=%#v", m.parallelGroups, m.parallelCalls)
	}
	if m.workflow != nil || m.workflowVisible {
		t.Fatalf("workflow state remains: workflow=%#v visible=%v", m.workflow, m.workflowVisible)
	}
	if m.turn != (turnStats{}) || m.activeTree != nil || m.treeIndex != nil {
		t.Fatalf("turn or tree state remains: turn=%+v active=%#v index=%#v", m.turn, m.activeTree, m.treeIndex)
	}
	if m.approval != nil || m.sendCancel != nil || m.pendingSteering != nil || m.queued != nil {
		t.Fatalf("pending activity remains: approval=%#v cancel=%v steering=%#v queued=%#v",
			m.approval, m.sendCancel != nil, m.pendingSteering, m.queued)
	}
	if !canceled {
		t.Fatal("in-flight context was not canceled")
	}
	select {
	case approved := <-approvalReply:
		if approved {
			t.Fatal("pending approval was approved during reset")
		}
	default:
		t.Fatal("pending approval waiter was not released")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("composer = %q, want empty", got)
	}
	if m.picker.visible || m.files.visible || m.files.token != (filePickerToken{}) || m.models.visible {
		t.Fatalf("transient UI remains: picker=%#v files=%#v models=%#v", m.picker, m.files, m.models)
	}
}

func TestResetConversationRetainsLongLivedSettings(t *testing.T) {
	m := makeTestModel()
	m.modelTag = "selected-model"
	m.providerTag = "openai"
	m.cwd = "~/Code/neo"
	m.branch = "feature"
	m.mdStyleName = "light"
	m.skills = []skills.Skill{{Name: "review", Body: "Review it"}}
	m.phases = []phase.Definition{{Name: "plan", Prompt: "Plan it"}}
	m.modelChoices = []ModelChoice{{ID: "selected-model", Name: "Selected"}}
	m.verbose = true
	m.files.root = "/workspace"
	m.files.files = []string{"cached.go"}
	m.files.err = errors.New("cached failure")
	afterSendCalls := 0
	m.afterSend = func() error {
		afterSendCalls++
		return nil
	}
	modelSwitcher := func(string) error { return nil }
	m.modelSwitcher = modelSwitcher
	steer := func(string) bool { return true }
	m.steer = steer
	originalContext := m.ctx
	originalAgent := m.ag

	m.handleSlashCommand("/clear")

	if m.ctx != originalContext || m.ag != originalAgent {
		t.Fatal("reset replaced the model context or agent")
	}
	if got := m.ag.Model(); got != "test" {
		t.Fatalf("agent model = %q, want retained test model", got)
	}
	if m.modelTag != "selected-model" || m.providerTag != "openai" || m.cwd != "~/Code/neo" || m.branch != "feature" {
		t.Fatalf("backend or workspace settings changed: model=%q provider=%q cwd=%q branch=%q",
			m.modelTag, m.providerTag, m.cwd, m.branch)
	}
	if m.mdStyleName != "light" || !m.verbose {
		t.Fatalf("presentation settings changed: style=%q verbose=%v", m.mdStyleName, m.verbose)
	}
	if !reflect.DeepEqual(m.skills, []skills.Skill{{Name: "review", Body: "Review it"}}) ||
		!reflect.DeepEqual(m.phases, []phase.Definition{{Name: "plan", Prompt: "Plan it"}}) ||
		!reflect.DeepEqual(m.modelChoices, []ModelChoice{{ID: "selected-model", Name: "Selected"}}) {
		t.Fatalf("commands or model choices changed: skills=%#v phases=%#v choices=%#v", m.skills, m.phases, m.modelChoices)
	}
	if m.files.root != "/workspace" || !reflect.DeepEqual(m.files.files, []string{"cached.go"}) || m.files.err != nil {
		t.Fatalf("file picker configuration/cache changed: %#v", m.files)
	}
	if m.modelSwitcher == nil || m.steer == nil {
		t.Fatal("long-lived callbacks were cleared")
	}
	if afterSendCalls != 1 {
		t.Fatalf("afterSend calls = %d, want 1", afterSendCalls)
	}
}

func TestResetConversationIgnoresBufferedActivityFromOldGeneration(t *testing.T) {
	m := makeTestModel()
	oldGeneration := m.conversationGeneration

	m.resetConversation()
	m.handleWorkflowEvent(workflow.Event{
		Generation: oldGeneration,
		Action:     "create",
		State: workflow.State{
			Title: "Old workflow",
			Items: []workflow.Item{{ID: "old", Text: "Old step"}},
		},
	})
	m.handleStepEvent(factory.Event{
		Generation: oldGeneration,
		Node:       1,
		Task:       "Old subagent",
		Ev:         factory.AgentEvent{Kind: "start"},
	})

	if m.workflow != nil || m.workflowVisible || m.activeTree != nil || m.treeIndex != nil || len(m.blocks) != 0 {
		t.Fatalf("old activity repopulated reset state: workflow=%#v visible=%v tree=%#v index=%#v blocks=%#v",
			m.workflow, m.workflowVisible, m.activeTree, m.treeIndex, m.blocks)
	}
}

func TestOldActivityStaysStaleAfterNewTurnStarts(t *testing.T) {
	m := makeTestModel()
	oldGeneration := m.conversationGeneration
	m.resetConversation()
	newGeneration := m.conversationGeneration
	send := m.submitUserTurn("new turn", "new turn", nil)
	t.Cleanup(func() {
		if m.sendCancel != nil {
			m.sendCancel()
		}
	})
	if send == nil {
		t.Fatal("new turn did not create a send command")
	}

	m.handleWorkflowEvent(workflow.Event{
		Generation: oldGeneration,
		Action:     "create",
		State: workflow.State{
			Title: "Old workflow",
			Items: []workflow.Item{{ID: "old", Text: "Old step"}},
		},
	})
	m.handleStepEvent(factory.Event{
		Generation: oldGeneration,
		Node:       1,
		Task:       "Old subagent",
		Ev:         factory.AgentEvent{Kind: "start"},
	})

	if m.conversationGeneration != newGeneration {
		t.Fatalf("new turn changed conversation generation: got %d want %d", m.conversationGeneration, newGeneration)
	}
	if m.workflow != nil || m.activeTree != nil || m.treeIndex != nil {
		t.Fatalf("old activity was relabeled for new turn: workflow=%#v tree=%#v index=%#v", m.workflow, m.activeTree, m.treeIndex)
	}
	if len(m.blocks) != 1 {
		t.Fatalf("old activity appended blocks to new turn: %#v", m.blocks)
	}
	if _, ok := m.blocks[0].(userBlock); !ok {
		t.Fatalf("new turn block = %T, want userBlock", m.blocks[0])
	}
}
