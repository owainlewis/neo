package tui

import (
	"strings"

	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/workflow"
)

// handleWorkflowEvent applies one workflow tool call to the live checklist.
// The checklist is an ordinary transcript block, appended when created and
// mutated in place afterwards; it never occupies fixed screen space.
func (m *model) handleWorkflowEvent(ev workflow.Event) {
	if ev.Generation != m.conversationGeneration {
		logx.Debug("stale workflow event ignored", "event_generation", ev.Generation, "conversation_generation", m.conversationGeneration)
		return
	}
	if ev.Action == "clear" {
		m.workflow = nil
		m.refreshViewport()
		return
	}
	m.turn.workflow = true
	if ev.Action == "create" {
		wb := &workflowBlock{title: ev.State.Title, items: ev.State.Items}
		m.workflow = wb
		m.appendBlock(wb)
		return
	}
	if m.workflow == nil {
		return
	}
	for i := range m.workflow.items {
		if m.workflow.items[i].ID != ev.ID {
			continue
		}
		switch ev.Action {
		case "start":
			m.workflow.active = ev.ID
			m.workflow.items[i].Status = workflow.Running
		case "complete":
			if m.workflow.active == ev.ID {
				m.workflow.active = ""
			}
			m.workflow.items[i].Status = workflow.Done
		case "fail":
			if m.workflow.active == ev.ID {
				m.workflow.active = ""
			}
			m.workflow.items[i].Status = workflow.Failed
		case "skip":
			if m.workflow.active == ev.ID {
				m.workflow.active = ""
			}
			m.workflow.items[i].Status = workflow.Skipped
		}
		if ev.Detail != "" {
			m.workflow.items[i].Detail = ev.Detail
		}
		m.refreshViewport()
		return
	}
}

// noteWorkflowActivity attaches the latest tool activity to the running item
// so the checklist shows what the agent is doing for that step.
func (m *model) noteWorkflowActivity(detail string) {
	if m.workflow == nil || m.workflow.active == "" || strings.TrimSpace(detail) == "" {
		return
	}
	for i := range m.workflow.items {
		if m.workflow.items[i].ID == m.workflow.active {
			m.workflow.items[i].Detail = detail
			m.refreshViewport()
			return
		}
	}
}
