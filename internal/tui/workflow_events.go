package tui

import (
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/factory"
	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/workflow"
)

func (m *model) handleWorkflowEvent(ev workflow.Event) {
	if ev.Action == "clear" {
		m.workflow = nil
		m.workflowVisible = false
		m.layout()
		m.refreshViewport()
		return
	}
	m.turn.workflow = true
	if ev.Action == "create" {
		wb := &workflowBlock{title: ev.State.Title, items: ev.State.Items}
		m.workflow = wb
		m.workflowVisible = false
		m.layout()
		m.refreshViewport()
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

// handleStepEvent folds the supervisor's event stream into activity blocks.
func (m *model) handleStepEvent(ev factory.Event) {
	if m.handleParallelStepEvent(ev) {
		return
	}
	switch ev.Ev.Kind {
	case "start":
		m.startTreeNode(ev)
	case "done", "fail":
		tb := m.treeIndex[ev.Node]
		if tb == nil {
			return
		}
		if n := tb.nodes[ev.Node]; n != nil && !n.done {
			n.done = true
			n.ok = ev.Ev.Kind == "done"
			n.elapsed = time.Since(n.startAt)
			n.lastLine = ""
			m.refreshViewport()
		}
	case "tool", "text", "error":
		tb := m.treeIndex[ev.Node]
		if tb == nil {
			return
		}
		if n := tb.nodes[ev.Node]; n != nil && !n.done {
			if line := strings.TrimSpace(ev.Ev.Body); line != "" {
				n.lastLine = line
				m.refreshViewport()
			}
		}
	}
}

func (m *model) handleParallelStepEvent(ev factory.Event) bool {
	if ev.GroupID == "" || ev.CallID == "" {
		return false
	}
	row := m.parallelCalls[ev.CallID]
	if row == nil || row.groupID != ev.GroupID {
		logx.Debug("unknown parallel subagent event ignored", "group_id", ev.GroupID, "call_id", ev.CallID, "node", ev.Node)
		return true
	}
	// The parent tool result is authoritative. Supervisor events arrive on a
	// separate stream and may be delayed, duplicated, or dropped, so none may
	// rewrite a row after its parent result has settled it.
	if row.parentSettled {
		return true
	}
	switch ev.Ev.Kind {
	case "start":
		// A retry gets a new supervisor node for the same parent tool call.
		// Restore the preallocated row and ignore late terminal events from the
		// previous attempt by remembering the current node.
		if row.state == parallelFailed {
			row.state = parallelRunning
			row.startAt = time.Now()
			row.elapsed = 0
			row.detail = ""
		}
		row.nodeID = ev.Node
		if strings.TrimSpace(ev.Task) != "" {
			row.args = map[string]any{"prompt": ev.Task}
		}
	case "done", "fail":
		if row.nodeID != 0 && row.nodeID != ev.Node {
			return true
		}
		if row.state == parallelRunning {
			row.elapsed = time.Since(row.startAt)
			if ev.Ev.Kind == "done" {
				row.state = parallelSucceeded
			} else {
				row.state = parallelFailed
			}
			row.detail = ""
		}
	case "tool", "text", "error":
		if row.state == parallelRunning {
			row.detail = strings.TrimSpace(ev.Ev.Body)
		}
	}
	m.refreshViewport()
	return true
}

// startTreeNode places a started agent in the current activity block.
func (m *model) startTreeNode(ev factory.Event) {
	if m.treeIndex == nil {
		m.treeIndex = map[int]*treeBlock{}
	}
	node := &treeNode{id: ev.Node, task: ev.Task, startAt: time.Now()}
	if m.activeTree == nil || len(m.blocks) == 0 || m.blocks[len(m.blocks)-1] != block(m.activeTree) {
		m.activeTree = newTreeBlock()
		m.appendBlock(m.activeTree)
	}
	m.activeTree.roots = append(m.activeTree.roots, ev.Node)
	m.activeTree.nodes[ev.Node] = node
	m.treeIndex[ev.Node] = m.activeTree
	m.refreshViewport()
}
