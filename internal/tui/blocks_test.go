package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

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

func TestToolCallBlockRendersConciseReceiptByDefault(t *testing.T) {
	out := plain(toolCallBlock{
		name: "read_file", args: map[string]any{"path": "internal/tui/model.go"}, elapsed: 2 * time.Second,
	}.render(80, nil))
	if out != "· Read internal/tui/model.go  2s" {
		t.Fatalf("concise tool call render = %q, want a completed receipt", out)
	}
	if strings.Contains(out, "✓") {
		t.Fatalf("routine tool receipt should not render as a completed milestone: %q", out)
	}
}

func TestConsecutiveToolReceiptsRenderAsTightTimeline(t *testing.T) {
	read := toolCallBlock{name: "read_file", args: map[string]any{"path": "a.go"}}
	edit := toolCallBlock{name: "edit_file", args: map[string]any{"path": "a.go"}}
	if gap := blockSeparator(read, edit); gap != "" {
		t.Fatalf("consecutive tool receipt gap = %q, want tight timeline", gap)
	}
	if gap := blockSeparator(edit, textBlock{text: "Done."}); gap != "\n" {
		t.Fatalf("tool-to-response gap = %q, want semantic separation", gap)
	}
}

func TestSubsecondActivityUsesReadableElapsedTime(t *testing.T) {
	if got := formatElapsed(250 * time.Millisecond); got != "<1s" {
		t.Fatalf("formatElapsed(250ms) = %q, want <1s", got)
	}
}

func TestToolCallBlockRendersFullCardWhenVerbose(t *testing.T) {
	out := plain(toolCallBlock{name: "read_file", args: map[string]any{"path": "internal/tui/model.go"}, verbose: true}.render(80, nil))
	if !strings.Contains(out, "read internal/tui/model.go") {
		t.Fatalf("verbose tool call render missing card header: %q", out)
	}
	if strings.Contains(out, "Read internal/tui/model.go") {
		t.Fatalf("verbose render should not use the concise receipt: %q", out)
	}
}

func TestToolReceiptLineCoversRoutineTools(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"bash", map[string]any{"command": "npm test"}, "Ran npm test"},
		{"read_file", map[string]any{"path": "a.go"}, "Read a.go"},
		{"write_file", map[string]any{"path": "a.go"}, "Wrote a.go"},
		{"edit_file", map[string]any{"path": "a.go"}, "Edited a.go"},
		{"grep", map[string]any{"pattern": "TODO"}, "Searched TODO"},
		{"glob", map[string]any{"pattern": "**/*.go"}, "Matched **/*.go"},
	}
	for _, tc := range cases {
		if got := toolReceiptLine(tc.name, tc.args); got != tc.want {
			t.Fatalf("toolReceiptLine(%q) = %q, want %q", tc.name, got, tc.want)
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

	if len(m.blocks) != 1 {
		t.Fatalf("blocks = %d, want only failure without a success receipt: %#v", len(m.blocks), m.blocks)
	}
	result, ok := m.blocks[0].(toolResultBlock)
	if !ok || !result.isError || result.text != "exit 1" {
		t.Fatalf("failure result = %#v", m.blocks[0])
	}
}

func TestWorkflowToolFailureRendersAndMarksTurnFailed(t *testing.T) {
	m := makeTestModel()
	m.handleEvent(agent.Event{Kind: agent.EventToolCall, Name: "workflow", Args: map[string]any{"action": "create"}})
	m.handleEvent(agent.Event{Kind: agent.EventToolResult, Name: "workflow", Text: "invalid workflow action", IsError: true})

	if len(m.blocks) != 1 {
		t.Fatalf("blocks = %d, want workflow failure: %#v", len(m.blocks), m.blocks)
	}
	result, ok := m.blocks[0].(toolResultBlock)
	if !ok || !result.isError || result.text != "invalid workflow action" {
		t.Fatalf("workflow failure = %#v", m.blocks[0])
	}
	if m.turn.errors != 1 {
		t.Fatalf("turn errors = %d, want 1", m.turn.errors)
	}
	summary, ok := m.resultSummary(nil, time.Second)
	if !ok || !summary.failed || summary.label != "Finished with issues" {
		t.Fatalf("workflow failure summary = %#v, ok=%v", summary, ok)
	}
}

func TestTranscriptReplayRespectsOutputModeAndKeepsFailures(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "tool_use", ID: "read-1", Name: "read_file", Input: map[string]any{"path": "main.go"}}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "tool_result", ToolUseID: "read-1", Content: "package main"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "tool_use", ID: "bash-1", Name: "bash", Input: map[string]any{"command": "false"}}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "tool_result", ToolUseID: "bash-1", Content: "exit 1", IsError: true}}},
	}

	concise := makeTestModel()
	concise.appendTranscript(messages)
	if len(concise.blocks) != 2 {
		t.Fatalf("concise replay blocks = %d, want successful call and failure: %#v", len(concise.blocks), concise.blocks)
	}
	if result, ok := concise.blocks[1].(toolResultBlock); !ok || !result.isError {
		t.Fatalf("concise replay did not preserve failure: %#v", concise.blocks[1])
	}
	for _, block := range concise.blocks {
		if got := plain(block.render(80, nil)); strings.Contains(got, "· Ran false") {
			t.Fatalf("concise replay marked failed tool successful: %q", got)
		}
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

func TestTranscriptReplayPairsRepeatedAndEmptyToolIDsByOccurrence(t *testing.T) {
	tests := []struct {
		name     string
		messages []llm.Message
		want     []string
		reject   string
	}{
		{
			name: "repeated synthesized ID",
			messages: []llm.Message{
				{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "tool_use", ID: "bash", Name: "bash", Input: map[string]any{"command": "true"}}}},
				{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "tool_result", ToolUseID: "bash", Content: ""}}},
				{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "tool_use", ID: "bash", Name: "bash", Input: map[string]any{"command": "false"}}}},
				{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "tool_result", ToolUseID: "bash", Content: "exit 1", IsError: true}}},
			},
			want:   []string{"· Ran true", "exit 1"},
			reject: "· Ran false",
		},
		{
			name: "legacy empty ID",
			messages: []llm.Message{
				{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "tool_use", Name: "bash", Input: map[string]any{"command": "false"}}}},
				{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "tool_result", Content: "exit 1", IsError: true}}},
			},
			want:   []string{"exit 1"},
			reject: "· Ran false",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := makeTestModel()
			m.appendTranscript(tc.messages)
			var rendered strings.Builder
			for _, block := range m.blocks {
				rendered.WriteString(plain(block.render(80, nil)))
				rendered.WriteByte('\n')
			}
			got := rendered.String()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("replay missing %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, tc.reject) {
				t.Fatalf("replay contains %q:\n%s", tc.reject, got)
			}
		})
	}
}

func TestToolPreambleRendersAsQuietCommentaryLiveAndOnReplay(t *testing.T) {
	m := makeTestModel()
	m.handleEvent(agent.Event{Kind: agent.EventAssistantCommentary, Text: "I’ll inspect the load path first."})
	if len(m.blocks) != 1 {
		t.Fatalf("live blocks = %d, want 1", len(m.blocks))
	}
	if _, ok := m.blocks[0].(thinkingBlock); !ok {
		t.Fatalf("live commentary block = %T, want thinkingBlock", m.blocks[0])
	}
	if got := strings.TrimSpace(plain(m.blocks[0].render(80, nil))); got != "I’ll inspect the load path first." {
		t.Fatalf("live commentary render = %q", got)
	}
	if raw := m.blocks[0].render(80, nil); strings.Contains(raw, "\x1b[3m") {
		t.Fatalf("commentary should not use terminal italics: %q", raw)
	}

	replay := makeTestModel()
	replay.appendTranscript([]llm.Message{{
		Role: llm.RoleAssistant,
		Content: []llm.ContentBlock{
			{Type: "text", Text: "I’ll inspect the load path first."},
			{Type: "tool_use", Name: "read_file", Input: map[string]any{"path": "store.go"}},
		},
	}})
	if len(replay.blocks) != 2 {
		t.Fatalf("replay blocks = %d, want commentary and receipt", len(replay.blocks))
	}
	if _, ok := replay.blocks[0].(thinkingBlock); !ok {
		t.Fatalf("replayed commentary block = %T, want thinkingBlock", replay.blocks[0])
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

	for _, want := range []string{"Plan  2/2", "✓ Inspect", "updated status line"} {
		if !strings.Contains(out, want) {
			t.Fatalf("workflow render missing %q:\n%s", want, out)
		}
	}
}

func TestWorkflowBlockHeightIsStableAndRowsDoNotWrap(t *testing.T) {
	const width = 32
	block := &workflowBlock{
		title: "A deliberately long\nworkflow title",
		items: []workflow.Item{
			{ID: "1", Text: "Inspect a deliberately\nlong subsystem name", Status: workflow.Pending},
			{ID: "2", Text: "Implement", Status: workflow.Running, Detail: "Editing a/very/long/path/to/model.go"},
		},
	}

	assertStable := func(state string) {
		t.Helper()
		lines := strings.Split(block.render(width, nil), "\n")
		if len(lines) != 1+len(block.items) {
			t.Fatalf("%s workflow lines = %d, want %d:\n%s", state, len(lines), 1+len(block.items), plain(strings.Join(lines, "\n")))
		}
		for i, line := range lines {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("%s workflow line %d width = %d, want <= %d: %q", state, i, got, width, plain(line))
			}
		}
	}

	assertStable("running")
	block.items[0].Status = workflow.Done
	block.items[1].Status = workflow.Done
	assertStable("complete")
}

func TestStatusLineShowsOneLineOfRealActivity(t *testing.T) {
	m := makeTestModel()
	m.busy = true
	m.busySince = time.Now().Add(-4 * time.Second)
	m.currentTool = &toolCallBlock{name: "read_file", args: map[string]any{"path": "internal/tui/model.go"}}

	out := plain(m.statusLine())
	for _, want := range []string{"● Reading internal/tui/model.go · 4s", "↩ steer", "ctrl+↩ queue"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status line missing %q: %q", want, out)
		}
	}

	m.currentTool = nil
	out = plain(m.statusLine())
	if !strings.Contains(out, "Thinking · 4s") || strings.Contains(out, "Reading") {
		t.Fatalf("generic working state should not invent activity: %q", out)
	}
}

func TestStatusLineCombinesWorkflowStepAndToolActivity(t *testing.T) {
	m := makeTestModel()
	m.width = 120
	m.busy = true
	m.busySince = time.Now().Add(-7 * time.Second)
	m.workflow = &workflowBlock{title: "Polish progress", items: []workflow.Item{
		{ID: "1", Text: "Inspect", Status: workflow.Done},
		{ID: "2", Text: "Refine progress UI", Status: workflow.Running},
		{ID: "3", Text: "Verify", Status: workflow.Pending},
	}}
	m.currentTool = &toolCallBlock{name: "edit_file", args: map[string]any{"path": "internal/tui/blocks.go"}}

	out := plain(m.statusLine())
	for _, want := range []string{"2/3 Refine progress UI", "Editing internal/tui/blocks.go", "7s", "tab plan"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status line missing %q: %q", want, out)
		}
	}
}

func TestStatusLinePreservesElapsedAndControlsWithLongActivity(t *testing.T) {
	m := makeTestModel()
	m.width = 80
	m.busy = true
	m.busySince = time.Now().Add(-9 * time.Second)
	m.workflow = &workflowBlock{items: []workflow.Item{
		{ID: "1", Text: "Implement a deliberately long workflow step description", Status: workflow.Running},
	}}
	m.currentTool = &toolCallBlock{name: "edit_file", args: map[string]any{
		"path": "a/very/long/path/to/internal/tui/progress_renderer.go",
	}}

	out := plain(m.statusLine())
	for _, want := range []string{"9s", "tab plan", "esc interrupt"} {
		if !strings.Contains(out, want) {
			t.Fatalf("long status line hid %q: %q", want, out)
		}
	}
	if got := len([]rune(out)); got > m.width {
		t.Fatalf("status width = %d, want <= %d: %q", got, m.width, out)
	}
}

func TestStatusLineKeepsApprovalControlWithWorkflow(t *testing.T) {
	m := makeTestModel()
	m.width = 48
	m.busy = true
	m.busySince = time.Now()
	m.workflow = &workflowBlock{items: []workflow.Item{{ID: "1", Text: "Inspect", Status: workflow.Running}}}
	m.approval = &approvalState{}

	out := plain(m.statusLine())
	for _, want := range []string{"Waiting for", "tab plan", "esc deny"} {
		if !strings.Contains(out, want) {
			t.Fatalf("approval status missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "interrupt") {
		t.Fatalf("approval status advertises interrupt instead of deny: %q", out)
	}
}

func TestStatusSpinnerNeverDisappears(t *testing.T) {
	for i, frame := range statusSpinner.Frames {
		if strings.TrimSpace(frame) == "" {
			t.Fatalf("spinner frame %d is blank", i)
		}
	}
}

func TestStatusLineHeightTracksActivityDetail(t *testing.T) {
	m := makeTestModel()
	if got := m.statusLineHeight(); got != 1 {
		t.Fatalf("idle status height = %d, want 1", got)
	}
	m.busy = true
	if got := m.statusLineHeight(); got != 1 {
		t.Fatalf("generic working status height = %d, want 1", got)
	}
	m.currentTool = &toolCallBlock{name: "read_file"}
	if got := m.statusLineHeight(); got != 1 {
		t.Fatalf("activity status height = %d, want 1", got)
	}
}

func TestStatusLineUsesApprovalHintAndFitsNarrowWidth(t *testing.T) {
	m := makeTestModel()
	m.width = 80
	m.busy = true
	m.busySince = time.Now()
	m.currentTool = &toolCallBlock{
		name: "read_file",
		args: map[string]any{"path": "a/very/long/path/to/internal/tui/model.go"},
	}
	m.approval = &approvalState{}

	approvalLine := strings.Split(plain(m.statusLine()), "\n")[0]
	if !strings.Contains(approvalLine, "esc to deny") {
		t.Fatalf("approval status missing deny hint: %q", approvalLine)
	}
	if strings.Contains(approvalLine, "interrupt") {
		t.Fatalf("approval status advertises the wrong esc action: %q", approvalLine)
	}

	m.width = 24
	m.approval = nil
	lines := strings.Split(plain(m.statusLine()), "\n")
	if len(lines) != 1 {
		t.Fatalf("status lines = %d, want 1: %q", len(lines), lines)
	}
	for i, line := range lines {
		if got := len([]rune(line)); got > m.width {
			t.Fatalf("status line %d width = %d, want <= %d: %q", i, got, m.width, line)
		}
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
