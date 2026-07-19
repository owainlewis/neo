package tui

import (
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/factory"
	"github.com/owainlewis/neo/internal/workflow"
)

func (m *model) handleWorkflowEvent(ev workflow.Event) {
	if ev.Action == "clear" {
		m.workflow = nil
		m.workflowVisible = true
		m.layout()
		m.refreshViewport()
		return
	}
	m.turn.workflow = true
	if ev.Action == "create" {
		wb := &workflowBlock{title: ev.State.Title, items: ev.State.Items}
		m.workflow = wb
		m.workflowVisible = true
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

func toolActivity(name string, args map[string]any) string {
	switch name {
	case "bash":
		if cmd, ok := args["command"].(string); ok && strings.TrimSpace(cmd) != "" {
			return "$ " + oneLine(cmd)
		}
	case "read_file", "write_file":
		if p, ok := args["path"].(string); ok && strings.TrimSpace(p) != "" {
			return name + " " + p
		}
	case "edit_file":
		if p, ok := args["path"].(string); ok && strings.TrimSpace(p) != "" {
			return "edit " + p
		}
	case "grep":
		if pat, ok := args["pattern"].(string); ok && strings.TrimSpace(pat) != "" {
			return "grep " + pat
		}
	case "glob":
		if pat, ok := args["pattern"].(string); ok && strings.TrimSpace(pat) != "" {
			return "glob " + pat
		}
	case "agent":
		if prompt, ok := args["prompt"].(string); ok && strings.TrimSpace(prompt) != "" {
			return "agent " + oneLine(prompt)
		}
	}
	return name
}

// handleStepEvent folds the supervisor's event stream into tree blocks:
// "start" places a node (a fresh block per top-level call unless the
// previous block is still the active tree), "done"/"fail" settle it, and
// everything else updates the node's live status line.
func (m *model) handleStepEvent(ev factory.Event) {
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

// startTreeNode places a started node in a tree block. Top-level calls
// (children of the chat agent, node 0) root a block; deeper nodes attach
// under their parent's block wherever it lives.
func (m *model) startTreeNode(ev factory.Event) {
	if m.treeIndex == nil {
		m.treeIndex = map[int]*treeBlock{}
	}
	node := &treeNode{id: ev.Node, parent: ev.Parent, step: ev.Step, task: ev.Task, startAt: time.Now()}
	if ev.Parent == 0 {
		if m.activeTree == nil || len(m.blocks) == 0 || m.blocks[len(m.blocks)-1] != block(m.activeTree) {
			m.activeTree = newTreeBlock()
			m.appendBlock(m.activeTree)
		}
		m.activeTree.roots = append(m.activeTree.roots, ev.Node)
		m.activeTree.nodes[ev.Node] = node
		m.treeIndex[ev.Node] = m.activeTree
		m.refreshViewport()
		return
	}
	tb := m.treeIndex[ev.Parent]
	if tb == nil {
		return // parent unknown (e.g. events from a pre-resume session)
	}
	tb.nodes[ev.Node] = node
	tb.children[ev.Parent] = append(tb.children[ev.Parent], ev.Node)
	m.treeIndex[ev.Node] = tb
	m.refreshViewport()
}
