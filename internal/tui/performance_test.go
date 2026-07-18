package tui

import (
	"fmt"
	"testing"

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
