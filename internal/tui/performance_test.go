package tui

import (
	"fmt"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"

	"github.com/owainlewis/neo/internal/workflow"
)

// BenchmarkWorkflowRender measures a representative pure TUI render path.
// Full terminal frames and markdown rendering remain informational because
// terminal width and renderer behavior vary by environment.
func BenchmarkWorkflowRender(b *testing.B) {
	statuses := []workflow.Status{workflow.Pending, workflow.Running, workflow.Done, workflow.Failed}
	items := make([]workflow.Item, 24)
	for i := range items {
		items[i] = workflow.Item{
			ID:     fmt.Sprintf("step-%d", i),
			Text:   fmt.Sprintf("Implement focused change %d", i),
			Status: statuses[i%len(statuses)],
			Detail: "internal/tui/model.go",
		}
	}
	block := workflowBlock{title: "Ship the change", items: items}
	b.ReportAllocs()

	for b.Loop() {
		if rendered := block.render(100, nil); len(rendered) == 0 {
			b.Fatal("empty workflow render")
		}
	}
}

func BenchmarkLargeTranscriptRefresh(b *testing.B) {
	blocks := make([]block, 400)
	for i := range blocks {
		blocks[i] = textBlock{text: fmt.Sprintf("Completed repository task %d with focused checks and review.", i)}
	}
	m := model{
		width:    100,
		blocks:   blocks,
		viewport: viewport.New(viewport.WithWidth(100), viewport.WithHeight(40)),
	}
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		m.refreshViewport()
	}
}

func BenchmarkActiveSubagentTreeRender(b *testing.B) {
	tree := newTreeBlock()
	now := time.Now()
	for root := 1; root <= 8; root++ {
		tree.roots = append(tree.roots, root)
		tree.nodes[root] = &treeNode{id: root, step: "worker", task: fmt.Sprintf("task %d", root), startAt: now}
		for child := 1; child <= 7; child++ {
			id := root*100 + child
			tree.nodes[id] = &treeNode{id: id, parent: root, step: "check", task: fmt.Sprintf("subtask %d", child), startAt: now, lastLine: "running focused verification"}
			tree.children[root] = append(tree.children[root], id)
		}
	}
	b.ReportAllocs()

	for b.Loop() {
		if rendered := tree.render(100, nil); len(rendered) == 0 {
			b.Fatal("empty subagent tree render")
		}
	}
}
