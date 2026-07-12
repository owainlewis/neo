package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/glamour/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/workflow"
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
	if want := "Paused after 50 steps. Reply to continue."; !strings.Contains(out, want) {
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

func TestToolCallBlockRendersConciseStatusLineByDefault(t *testing.T) {
	out := plain(toolCallBlock{name: "read_file", args: map[string]any{"path": "internal/tui/model.go"}}.render(80, nil))
	if out != "Reading internal/tui/model.go..." {
		t.Fatalf("concise tool call render = %q, want a plain status line", out)
	}
}

func TestToolCallBlockRendersFullCardWhenVerbose(t *testing.T) {
	out := plain(toolCallBlock{name: "read_file", args: map[string]any{"path": "internal/tui/model.go"}, verbose: true}.render(80, nil))
	if !strings.Contains(out, "read internal/tui/model.go") {
		t.Fatalf("verbose tool call render missing card header: %q", out)
	}
	if strings.Contains(out, "Reading internal/tui/model.go...") {
		t.Fatalf("verbose render should not use the concise status line: %q", out)
	}
}

func TestToolStatusLineCoversRoutineTools(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"bash", map[string]any{"command": "npm test"}, "Running npm test..."},
		{"read_file", map[string]any{"path": "a.go"}, "Reading a.go..."},
		{"write_file", map[string]any{"path": "a.go"}, "Writing a.go..."},
		{"edit_file", map[string]any{"path": "a.go"}, "Editing a.go..."},
		{"grep", map[string]any{"pattern": "TODO"}, "Searching TODO..."},
		{"glob", map[string]any{"pattern": "**/*.go"}, "Matching **/*.go..."},
	}
	for _, tc := range cases {
		if got := toolStatusLine(tc.name, tc.args); got != tc.want {
			t.Fatalf("toolStatusLine(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestToolEventsRenderSuccessfulResultsOnlyWhenVerbose(t *testing.T) {
	for _, tc := range []struct {
		name       string
		verbose    bool
		wantBlocks int
	}{
		{name: "concise", wantBlocks: 1},
		{name: "verbose", verbose: true, wantBlocks: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := makeTestModel()
			m.verbose = tc.verbose
			m.handleEvent(agent.Event{Kind: agent.EventToolCall, Name: "read_file", Args: map[string]any{"path": "main.go"}})
			m.handleEvent(agent.Event{Kind: agent.EventToolResult, Name: "read_file", Text: "package main"})

			if len(m.blocks) != tc.wantBlocks {
				t.Fatalf("blocks = %d, want %d: %#v", len(m.blocks), tc.wantBlocks, m.blocks)
			}
			call, ok := m.blocks[0].(toolCallBlock)
			if !ok || call.verbose != tc.verbose {
				t.Fatalf("tool call = %#v, want verbose=%v", m.blocks[0], tc.verbose)
			}
		})
	}
}

func TestToolEventsAlwaysRenderFailures(t *testing.T) {
	m := makeTestModel()
	m.handleEvent(agent.Event{Kind: agent.EventToolCall, Name: "bash", Args: map[string]any{"command": "false"}})
	m.handleEvent(agent.Event{Kind: agent.EventToolResult, Name: "bash", Text: "exit 1", IsError: true})

	if len(m.blocks) != 2 {
		t.Fatalf("blocks = %d, want call and failure: %#v", len(m.blocks), m.blocks)
	}
	result, ok := m.blocks[1].(toolResultBlock)
	if !ok || !result.isError || result.text != "exit 1" {
		t.Fatalf("failure result = %#v", m.blocks[1])
	}
}

func TestTranscriptReplayRespectsOutputModeAndKeepsFailures(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "tool_use", Name: "read_file", Input: map[string]any{"path": "main.go"}}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "tool_result", Content: "package main"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "tool_use", Name: "bash", Input: map[string]any{"command": "false"}}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "tool_result", Content: "exit 1", IsError: true}}},
	}

	concise := makeTestModel()
	concise.appendTranscript(messages)
	if len(concise.blocks) != 3 {
		t.Fatalf("concise replay blocks = %d, want two calls and one failure: %#v", len(concise.blocks), concise.blocks)
	}
	if result, ok := concise.blocks[2].(toolResultBlock); !ok || !result.isError {
		t.Fatalf("concise replay did not preserve failure: %#v", concise.blocks[2])
	}

	verbose := makeTestModel()
	verbose.verbose = true
	verbose.appendTranscript(messages)
	if len(verbose.blocks) != 4 {
		t.Fatalf("verbose replay blocks = %d, want calls and results: %#v", len(verbose.blocks), verbose.blocks)
	}
	for _, index := range []int{0, 2} {
		call, ok := verbose.blocks[index].(toolCallBlock)
		if !ok || !call.verbose {
			t.Fatalf("verbose replay call at %d = %#v", index, verbose.blocks[index])
		}
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

func TestWorkflowBlockRenderShowsProgressAndCompletion(t *testing.T) {
	out := plain((&workflowBlock{
		title: "Plan",
		items: []workflow.Item{
			{ID: "1", Text: "Inspect", Status: workflow.Done},
			{ID: "2", Text: "Polish", Status: workflow.Done, Detail: "updated status line"},
		},
	}).render(80, nil))

	for _, want := range []string{"Plan  2/2", "✓ Inspect", "updated status line", "Plan complete · 2/2 steps"} {
		if !strings.Contains(out, want) {
			t.Fatalf("workflow render missing %q:\n%s", want, out)
		}
	}
}

func TestStatusLineUsesSpecificNeoActivity(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.busySince = time.Now()
	m.currentTool = &toolCallBlock{name: "read_file", args: map[string]any{"path": "internal/tui/model.go"}}

	out := plain(m.statusLine())
	if !strings.Contains(out, "Neo is reading internal/tui/model.go") {
		t.Fatalf("status line missing specific activity: %q", out)
	}

	m.currentTool = nil
	out = plain(m.statusLine())
	if !strings.Contains(out, "Neo is thinking") {
		t.Fatalf("status line missing thinking activity: %q", out)
	}
}

func TestResultSummaryBlockRenderIsCompact(t *testing.T) {
	out := plain(resultSummaryBlock{label: "Done", detail: "2 tools", elapsed: 2 * time.Second}.render(80, nil))
	for _, want := range []string{"✓ Done", "2 tools", "2s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary render missing %q: %q", want, out)
		}
	}
}
