package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/glamour/v2"

	"github.com/owainlewis/neo/internal/agent"
)

func TestTextBlockRenderTrimsMarkdownEdgeNewlines(t *testing.T) {
	md, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	out := textBlock{text: "Assistant response"}.render(80, md)
	if strings.HasPrefix(out, "\n") {
		t.Fatalf("rendered text starts with a newline: %q", out)
	}
	if strings.HasSuffix(out, "\n") {
		t.Fatalf("rendered text ends with a newline: %q", out)
	}
	if !strings.Contains(plain(out), "Assistant response") {
		t.Fatalf("rendered text missing content: %q", out)
	}
}

func TestMaxTurnsBlockRenderShowsLimitAndContinuationHint(t *testing.T) {
	out := plain(maxTurnsBlock{limit: 50}.render(80, nil))
	if want := "hit turn limit (50). Reply to continue."; !strings.Contains(out, want) {
		t.Fatalf("rendered block missing %q: %q", want, out)
	}
}

func TestApprovalBlockRenderTruncatesLongPreview(t *testing.T) {
	var lines []string
	for i := 0; i < 80; i++ {
		lines = append(lines, fmt.Sprintf("+line %02d", i))
	}

	out := plain(approvalBlock{req: agent.ApprovalRequest{
		ToolName: "write_file",
		Args:     map[string]any{"path": "notes.md", "content": "new\ncontent"},
		Reason:   "file write requires approval",
		Preview:  strings.Join(lines, "\n"),
	}}.render(80, nil))

	if !strings.Contains(firstLine(out), "permission required") {
		t.Fatalf("approval prompt should stay on the first line, got:\n%s", out)
	}
	if strings.Contains(out, "+line 79") {
		t.Fatalf("approval preview was not truncated:\n%s", out)
	}
	for _, want := range []string{"write notes.md", "2 lines", "reason: file write requires approval", "keys: y approve", "ctrl+o to inspect"} {
		if !strings.Contains(out, want) {
			t.Fatalf("approval block missing %q:\n%s", want, out)
		}
	}
}

func TestApprovalBlockRenderKeepsShortPreview(t *testing.T) {
	out := plain(approvalBlock{req: testApproval("edit_file", "-old\n+new")}.render(80, nil))
	for _, want := range []string{"permission required", "edit", "keys: y approve", "-old", "+new"} {
		if !strings.Contains(out, want) {
			t.Fatalf("approval block missing %q:\n%s", want, out)
		}
	}
}

func TestApprovalBlockRenderExpandedLongPreview(t *testing.T) {
	var lines []string
	for i := 0; i < 80; i++ {
		lines = append(lines, fmt.Sprintf("+line %02d", i))
	}

	out := plain(approvalBlock{req: testApproval("edit_file", strings.Join(lines, "\n")), expanded: true}.render(80, nil))

	for _, want := range []string{"+line 79", "full preview", "ctrl+o to collapse"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expanded approval block missing %q:\n%s", want, out)
		}
	}
}

func TestToolResultBlockCanRenderCompactAndExpanded(t *testing.T) {
	text := numberedLines(15)

	compact := plain(toolResultBlock{text: text}.render(80, nil))
	if strings.Contains(compact, "line 14") {
		t.Fatalf("compact result should hide trailing output:\n%s", compact)
	}
	for _, want := range []string{"line 11", "+3 lines", "ctrl+o to expand"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact result missing %q:\n%s", want, compact)
		}
	}

	expanded := plain(toolResultBlock{text: text, expanded: true}.render(80, nil))
	for _, want := range []string{"line 14", "expanded", "ctrl+o to collapse"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded result missing %q:\n%s", want, expanded)
		}
	}

	short := plain(toolResultBlock{text: numberedLines(3)}.render(80, nil))
	if strings.Contains(short, "ctrl+o") {
		t.Fatalf("short result should not show expansion help:\n%s", short)
	}
}

func TestToggleLatestToolResultExpansionPreservesBlockOrder(t *testing.T) {
	m := makeTestModel()
	m.blocks = []block{
		textBlock{text: "before"},
		toolResultBlock{text: numberedLines(15)},
		textBlock{text: "after"},
	}

	if !m.toggleLatestToolResultExpansion() {
		t.Fatal("expected truncated tool result to toggle")
	}
	out := renderPlainBlocks(m)
	before := strings.Index(out, "before")
	last := strings.Index(out, "line 14")
	after := strings.Index(out, "after")
	if before < 0 || last < 0 || after < 0 || before >= last || last >= after {
		t.Fatalf("expanded output should stay between neighboring blocks:\n%s", out)
	}

	if !m.toggleLatestToolResultExpansion() {
		t.Fatal("expected expanded tool result to collapse")
	}
	collapsed := renderPlainBlocks(m)
	if strings.Contains(collapsed, "line 14") {
		t.Fatalf("collapsed output should hide trailing output:\n%s", collapsed)
	}
}

func TestToggleLatestToolResultExpansionIgnoresShortResults(t *testing.T) {
	m := makeTestModel()
	m.blocks = []block{toolResultBlock{text: numberedLines(3)}}
	if m.toggleLatestToolResultExpansion() {
		t.Fatal("short result should not toggle")
	}
}

func testApproval(tool, preview string) agent.ApprovalRequest {
	return agent.ApprovalRequest{ToolName: tool, Preview: preview}
}

func numberedLines(n int) string {
	lines := make([]string, 0, n)
	for i := 0; i < n; i++ {
		lines = append(lines, fmt.Sprintf("line %02d", i))
	}
	return strings.Join(lines, "\n")
}

func renderPlainBlocks(m *model) string {
	var sb strings.Builder
	for _, b := range m.blocks {
		sb.WriteString(plain(b.render(m.width, nil)))
		sb.WriteString("\n")
	}
	return sb.String()
}
