package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/skills"
	"github.com/owainlewis/neo/internal/tools"
)

func TestHelpBlock_ListsHelpCommandAndKeys(t *testing.T) {
	m := makeTestModel()
	out := plain(helpBlock{commands: m.slashCommands()}.render(80, nil))
	for _, want := range []string{"/help", "!cmd", "send", "newline", "pgup/pgdn", "ctrl+o", "quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("/help missing %q: %s", want, out)
		}
	}
}

func TestSlashCommand_HelpAppendsHelpBlock(t *testing.T) {
	m := makeTestModel()
	m.handleSlashCommand("/help")
	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	if _, ok := m.blocks[0].(helpBlock); !ok {
		t.Fatalf("expected helpBlock, got %T", m.blocks[0])
	}
}

func TestSlashCommand_MemoryAppendsProjectMemory(t *testing.T) {
	m := makeTestModel()
	m.projectRoot = t.TempDir()

	m.handleSlashCommand("/memory prefer table-driven tests")

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	nb, ok := m.blocks[0].(noticeBlock)
	if !ok {
		t.Fatalf("expected noticeBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(nb.text, "saved project memory") {
		t.Fatalf("unexpected notice: %q", nb.text)
	}
	got, err := os.ReadFile(filepath.Join(m.projectRoot, "memory.md"))
	if err != nil {
		t.Fatalf("read memory: %v", err)
	}
	if !strings.Contains(string(got), "prefer table-driven tests") {
		t.Fatalf("memory contents = %q", string(got))
	}
}

func TestSlashCommand_MemoryBlankShowsHelpfulError(t *testing.T) {
	m := makeTestModel()
	m.projectRoot = t.TempDir()

	m.handleSlashCommand("/memory")

	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "type text after /memory") {
		t.Fatalf("unexpected error: %v", eb.err)
	}
}

func TestSlashCommand_MemoryDisabledDoesNotWrite(t *testing.T) {
	m := makeTestModel()
	m.projectRoot = t.TempDir()
	m.memoryEnabled = false

	m.handleSlashCommand("/memory keep release notes in sync")

	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "unknown command: /memory") {
		t.Fatalf("unexpected error: %v", eb.err)
	}
	if _, err := os.Stat(filepath.Join(m.projectRoot, "memory.md")); !os.IsNotExist(err) {
		t.Fatalf("memory file should not exist, stat err = %v", err)
	}
}

func TestSlashCommand_MemoryReadonlyDoesNotWrite(t *testing.T) {
	m := makeTestModel()
	m.projectRoot = t.TempDir()
	m.permissionMode = "readonly"

	m.handleSlashCommand("/memory keep release notes in sync")

	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "readonly") {
		t.Fatalf("unexpected error: %v", eb.err)
	}
	if _, err := os.Stat(filepath.Join(m.projectRoot, "memory.md")); !os.IsNotExist(err) {
		t.Fatalf("memory file should not exist, stat err = %v", err)
	}
}

func TestSlashCommand_MemoryBusyDoesNotWrite(t *testing.T) {
	m := makeTestModel()
	m.projectRoot = t.TempDir()
	m.busy = true

	m.handleSlashCommand("/memory keep release notes in sync")

	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "while a turn is running") {
		t.Fatalf("unexpected error: %v", eb.err)
	}
	if _, err := os.Stat(filepath.Join(m.projectRoot, "memory.md")); !os.IsNotExist(err) {
		t.Fatalf("memory file should not exist, stat err = %v", err)
	}
}

func TestHelpBlock_HidesMemoryWhenDisabled(t *testing.T) {
	m := makeTestModel()
	m.memoryEnabled = false

	out := plain(helpBlock{commands: m.slashCommands()}.render(80, nil))

	if strings.Contains(out, "/memory") {
		t.Fatalf("help should hide /memory when disabled: %s", out)
	}
}

func TestSlashPicker_HidesMemoryWhenDisabled(t *testing.T) {
	m := makeTestModel()
	m.memoryEnabled = false
	m.input.SetValue("/")

	m.updateSlashPicker()

	for _, match := range m.picker.matches {
		if match.cmd == "/memory" {
			t.Fatalf("picker should hide /memory when disabled: %+v", m.picker.matches)
		}
	}
}

func TestSlashCommand_MemoryDisabledBehavesAsUnknown(t *testing.T) {
	m := makeTestModel()
	m.memoryEnabled = false

	m.handleSlashCommand("/memory keep release notes in sync")

	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "unknown command: /memory") {
		t.Fatalf("unexpected error: %v", eb.err)
	}
}

func TestSkillsAppearInHelpAndPicker(t *testing.T) {
	m := makeTestModel()
	m.skills = []skills.Skill{{Name: "review", Description: "review a diff", Body: "Review this."}}

	help := plain(helpBlock{commands: m.slashCommands()}.render(80, nil))
	if !strings.Contains(help, "/review") || !strings.Contains(help, "review a diff") {
		t.Fatalf("help missing skill command: %s", help)
	}

	m.input.SetValue("/r")
	m.updateSlashPicker()
	if len(m.picker.matches) != 1 || m.picker.matches[0].cmd != "/review" {
		t.Fatalf("expected /review picker match, got %+v", m.picker.matches)
	}
}

func TestSkillSlashInvocationExpandsBodyWithArguments(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("done")}}
	m := makeTestModel()
	m.ag = agent.New(agent.Config{Model: "test", Provider: prov, Policy: permission.New("trusted", ".")})
	m.skills = []skills.Skill{{Name: "review", Description: "review a diff", Body: "Review carefully."}}

	cmd := m.handleSlashCommand("/review internal/tui")
	if cmd == nil {
		t.Fatal("expected skill command to start a send")
	}
	m.Update(cmd())

	if len(prov.Calls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.Calls))
	}
	got := prov.Calls[0].Messages[len(prov.Calls[0].Messages)-1].Content[0].Text
	want := "[skill: review]\nReview carefully.\n\nArguments:\ninternal/tui"
	if got != want {
		t.Fatalf("sent prompt = %q", got)
	}
	if len(m.blocks) < 2 {
		t.Fatalf("expected visible command and notice blocks, got %d", len(m.blocks))
	}
	if _, ok := m.blocks[0].(userBlock); !ok {
		t.Fatalf("first block = %T, want userBlock", m.blocks[0])
	}
	foundNotice := false
	for _, b := range m.blocks {
		if nb, ok := b.(noticeBlock); ok && strings.Contains(nb.text, "applied skill: review") {
			foundNotice = true
		}
	}
	if !foundNotice {
		t.Fatalf("missing expansion notice: %+v", m.blocks)
	}
}

func TestSkillSlashInvocationDoesNotRescanExpandedBody(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("done")}}
	m := makeTestModel()
	m.ag = agent.New(agent.Config{Model: "test", Provider: prov, Policy: permission.New("trusted", ".")})
	m.skills = []skills.Skill{
		{Name: "review", Description: "review a diff", Body: "Mention $commit as an example."},
		{Name: "commit", Description: "write a commit", Body: "Commit instructions."},
	}

	cmd := m.handleSlashCommand("/review staged diff")
	if cmd == nil {
		t.Fatal("expected skill command to start a send")
	}
	m.Update(cmd())

	got := prov.Calls[0].Messages[len(prov.Calls[0].Messages)-1].Content[0].Text
	if strings.Contains(got, "Commit instructions.") {
		t.Fatalf("slash skill body should not be rescanned for skill refs, got:\n%s", got)
	}
	want := "[skill: review]\nMention $commit as an example.\n\nArguments:\nstaged diff"
	if got != want {
		t.Fatalf("sent prompt = %q, want %q", got, want)
	}
}

func TestSkillSlashCommandCannotOverrideBuiltInCommand(t *testing.T) {
	m := makeTestModel()
	m.skills = []skills.Skill{{Name: "help", Description: "custom help", Body: "not help"}}

	m.handleSlashCommand("/help")

	if len(m.blocks) != 1 {
		t.Fatalf("expected one help block, got %d", len(m.blocks))
	}
	if _, ok := m.blocks[0].(helpBlock); !ok {
		t.Fatalf("expected built-in help block, got %T", m.blocks[0])
	}
	if _, ok := m.slashSkill("/Help"); ok {
		t.Fatal("skill command should not override built-in names with different casing")
	}
	for _, c := range m.slashCommands() {
		if c.cmd == "/help" && c.desc == "custom help" {
			t.Fatal("skill command should not override built-in /help")
		}
	}
}

func TestSkillSlashCommandCannotAppearAsDisabledMemoryCommand(t *testing.T) {
	m := makeTestModel()
	m.memoryEnabled = false
	m.skills = []skills.Skill{{Name: "memory", Description: "custom memory", Body: "not memory"}}

	help := plain(helpBlock{commands: m.slashCommands()}.render(80, nil))
	if strings.Contains(help, "/memory") {
		t.Fatalf("help should not advertise disabled /memory skill: %s", help)
	}

	m.input.SetValue("/m")
	m.updateSlashPicker()
	for _, match := range m.picker.matches {
		if match.cmd == "/memory" {
			t.Fatalf("picker should not advertise disabled /memory skill: %+v", m.picker.matches)
		}
	}
}

func TestSlashCommand_UnknownSuggestsHelp(t *testing.T) {
	m := makeTestModel()
	m.handleSlashCommand("/wat")
	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "/help") {
		t.Fatalf("error should suggest /help, got %v", eb.err)
	}
}

func TestSlashCommand_ToolsPermissionsTokensModelAndClear(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		want string
	}{
		{"/tools", "read_file"},
		{"/tokens", "input: 0"},
		{"/model", "model: test"},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			m := makeTestModel()
			m.handleSlashCommand(tc.cmd)
			out := plain(m.blocks[0].render(80, nil))
			if !strings.Contains(out, tc.want) {
				t.Fatalf("%s output missing %q: %s", tc.cmd, tc.want, out)
			}
		})
	}

	m := makeTestModel()
	m.ag.SetUsage(llm.Usage{InputTokens: 1, OutputTokens: 2, CacheCreationTokens: 3, CacheReadTokens: 4})
	m.blocks = append(m.blocks, noticeBlock{text: "x"})
	m.handleSlashCommand("/clear")
	if len(m.blocks) != 0 {
		t.Fatalf("/clear left %d blocks", len(m.blocks))
	}
	if got := m.ag.Usage(); got != (llm.Usage{}) {
		t.Fatalf("/clear left usage = %+v", got)
	}
}

func TestSlashCommand_PermissionsOpensPicker(t *testing.T) {
	m := makeTestModel()
	m.handleSlashCommand("/permissions")

	if !m.perms.visible {
		t.Fatal("expected permissions picker to open")
	}
	out := plain(m.permissionPickerView())
	flatOut := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{
		"Select permission mode",
		"Current: ask",
		"bash and file mutations ask first",
		"workspace path checks still apply",
		"bash and file mutations are denied",
	} {
		if !strings.Contains(flatOut, want) {
			t.Fatalf("permissions picker missing %q: %s", want, out)
		}
	}
}

func TestPermissionPicker_SelectTrustedUpdatesSessionMode(t *testing.T) {
	m := makeTestModel()
	m.handleSlashCommand("/permissions")

	m.Update(keyPress(tea.KeyDown))
	m.Update(keyPress(tea.KeyEnter))

	if m.perms.visible {
		t.Fatal("expected permissions picker to close")
	}
	if m.permissionMode != "trusted" {
		t.Fatalf("permissionMode = %q, want trusted", m.permissionMode)
	}
	out := plain(m.blocks[0].render(80, nil))
	if !strings.Contains(out, "permissions: trusted") {
		t.Fatalf("expected trusted notice, got %s", out)
	}
}

func TestSlashCommand_ClearSavesSession(t *testing.T) {
	m := makeTestModel()
	calls := 0
	m.afterSend = func() error {
		calls++
		return nil
	}

	m.handleSlashCommand("/clear")

	if calls != 1 {
		t.Fatalf("afterSend calls = %d, want 1", calls)
	}
}

func TestSlashCommand_ClearShowsSaveError(t *testing.T) {
	m := makeTestModel()
	m.afterSend = func() error {
		return fmt.Errorf("save failed")
	}

	m.handleSlashCommand("/clear")

	if len(m.blocks) != 1 {
		t.Fatalf("expected save error block, got %d blocks", len(m.blocks))
	}
	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "save failed") {
		t.Fatalf("unexpected error: %v", eb.err)
	}
}

func TestSlashCommand_StatefulCommandsRequireIdle(t *testing.T) {
	for _, cmd := range []string{"/clear", "/tokens"} {
		t.Run(cmd, func(t *testing.T) {
			m := makeTestModel()
			m.busy = true
			m.blocks = append(m.blocks, noticeBlock{text: "keep"})

			m.handleSlashCommand(cmd)

			if len(m.blocks) != 2 {
				t.Fatalf("expected original block plus error, got %d", len(m.blocks))
			}
			if _, ok := m.blocks[0].(noticeBlock); !ok {
				t.Fatalf("first block changed to %T", m.blocks[0])
			}
			eb, ok := m.blocks[1].(errorBlock)
			if !ok {
				t.Fatalf("expected errorBlock, got %T", m.blocks[1])
			}
			if !strings.Contains(eb.err.Error(), "while a turn is running") {
				t.Fatalf("unexpected error: %v", eb.err)
			}
		})
	}
}

func TestApprovalPromptRepliesFromKeypress(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
		want bool
	}{
		{"approve", keyPress('y'), true},
		{"deny n", keyPress('n'), false},
		{"deny esc", keyPress(tea.KeyEsc), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := makeTestModel()
			reply := make(chan bool, 1)
			m.Update(approvalRequestMsg{
				req:   agent.ApprovalRequest{ToolName: "bash", Preview: "preview"},
				reply: reply,
			})
			if m.approval == nil {
				t.Fatal("expected pending approval")
			}
			m.Update(tt.key)
			if got := <-reply; got != tt.want {
				t.Fatalf("reply = %v, want %v", got, tt.want)
			}
			if m.approval != nil {
				t.Fatal("expected approval to clear")
			}
		})
	}
}

func TestApprovalPreviewToggleUpdatesPendingCard(t *testing.T) {
	m := makeTestModel()
	reply := make(chan bool, 1)
	m.Update(approvalRequestMsg{
		req:   agent.ApprovalRequest{ToolName: "edit_file", Preview: numberedLines(25)},
		reply: reply,
	})
	if !m.toggleApprovalPreview() {
		t.Fatal("expected approval preview to expand")
	}
	out := renderPlainBlocks(m)
	if !strings.Contains(out, "line 24") {
		t.Fatalf("expanded approval preview missing full content:\n%s", out)
	}
	if !m.toggleApprovalPreview() {
		t.Fatal("expected approval preview to collapse")
	}
	out = renderPlainBlocks(m)
	if strings.Contains(out, "line 24") {
		t.Fatalf("collapsed approval preview should hide full content:\n%s", out)
	}
}

func TestApprovalAlwaysAllowSkipsLaterPrompts(t *testing.T) {
	m := makeTestModel()

	// First call: grant "always" for this bash command.
	reply := make(chan bool, 1)
	m.Update(approvalRequestMsg{
		req:   agent.ApprovalRequest{ToolName: "bash", Args: map[string]any{"command": "go test ./..."}},
		reply: reply,
	})
	if m.approval == nil {
		t.Fatal("expected first call to prompt")
	}
	m.Update(keyPress('a'))
	if got := <-reply; !got {
		t.Fatal("expected always-allow to approve the call")
	}

	// A later go test invocation must auto-approve without a prompt.
	reply2 := make(chan bool, 1)
	m.Update(approvalRequestMsg{
		req:   agent.ApprovalRequest{ToolName: "bash", Args: map[string]any{"command": "go test -run X"}},
		reply: reply2,
	})
	if m.approval != nil {
		t.Fatal("granted command should not prompt again")
	}
	if got := <-reply2; !got {
		t.Fatal("granted command should auto-approve")
	}

	// An unrelated command still prompts.
	reply3 := make(chan bool, 1)
	m.Update(approvalRequestMsg{
		req:   agent.ApprovalRequest{ToolName: "bash", Args: map[string]any{"command": "rm -rf build"}},
		reply: reply3,
	})
	if m.approval == nil {
		t.Fatal("unrelated command should still prompt")
	}
}

func TestBangCommand_EmptyShowsHelpfulError(t *testing.T) {
	m := makeTestModel()

	cmd := m.handleBangCommand("!")

	if cmd != nil {
		t.Fatal("empty ! should not start a command")
	}
	if len(m.blocks) != 1 {
		t.Fatalf("expected one error block, got %d", len(m.blocks))
	}
	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "!git status") {
		t.Fatalf("expected helpful example, got %v", eb.err)
	}
}

func TestBangCommand_RunsBashThroughToolEventsWithoutProviderCall(t *testing.T) {
	prov := &llmtest.FakeProvider{}
	ag := agent.New(agent.Config{
		Model:    "test",
		Provider: prov,
		Tools:    tools.NewRegistry(tuiEchoTool{}),
		Policy:   permission.New("trusted", "."),
	})
	m := makeTestModel()
	m.ag = ag
	m.ag.SetEventHandler(m.handleEvent)

	cmd := m.handleBangCommand("!echo hello")
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	m.Update(msg)

	if m.busy {
		t.Fatal("expected command to finish")
	}
	if len(prov.Calls) != 0 {
		t.Fatalf("provider calls = %d, want 0", len(prov.Calls))
	}
	if got := len(m.ag.Transcript()); got != 0 {
		t.Fatalf("agent transcript length = %d, want 0", got)
	}
	if len(m.blocks) != 3 {
		t.Fatalf("expected tool call, result, and summary blocks, got %d", len(m.blocks))
	}
	tc, ok := m.blocks[0].(toolCallBlock)
	if !ok {
		t.Fatalf("expected toolCallBlock, got %T", m.blocks[0])
	}
	if tc.name != "bash" || tc.args["command"] != "echo hello" {
		t.Fatalf("unexpected tool call: %+v", tc)
	}
	tr, ok := m.blocks[1].(toolResultBlock)
	if !ok {
		t.Fatalf("expected toolResultBlock, got %T", m.blocks[1])
	}
	if tr.isError || tr.text != "echo hello" {
		t.Fatalf("unexpected tool result: %+v", tr)
	}
	rs, ok := m.blocks[2].(resultSummaryBlock)
	if !ok {
		t.Fatalf("expected resultSummaryBlock, got %T", m.blocks[2])
	}
	if rs.label != "Done" || !strings.Contains(rs.detail, "command complete") {
		t.Fatalf("unexpected summary: %+v", rs)
	}
}

func TestBangCommand_ConciseModeKeepsRequestedCommandOutput(t *testing.T) {
	prov := &llmtest.FakeProvider{}
	ag := agent.New(agent.Config{
		Model:    "test",
		Provider: prov,
		Tools:    tools.NewRegistry(tuiEchoTool{}),
		Policy:   permission.New("trusted", "."),
	})
	m := makeTestModel() // verbose defaults to false
	m.ag = ag
	m.ag.SetEventHandler(m.handleEvent)

	cmd := m.handleBangCommand("!echo hello")
	if cmd == nil {
		t.Fatal("expected command")
	}
	m.Update(cmd())

	if len(m.blocks) != 3 {
		t.Fatalf("expected tool call, result, and summary blocks, got %d: %+v", len(m.blocks), m.blocks)
	}
	tc, ok := m.blocks[0].(toolCallBlock)
	if !ok {
		t.Fatalf("expected toolCallBlock, got %T", m.blocks[0])
	}
	if tc.verbose {
		t.Fatal("expected concise tool call block")
	}
	if !strings.Contains(tc.render(80, nil), "Running echo hello...") {
		t.Fatalf("expected concise status line, got %q", tc.render(80, nil))
	}
	tr, ok := m.blocks[1].(toolResultBlock)
	if !ok || tr.text != "echo hello" || tr.isError {
		t.Fatalf("expected successful command output, got %#v", m.blocks[1])
	}
	if _, ok := m.blocks[2].(resultSummaryBlock); !ok {
		t.Fatalf("expected resultSummaryBlock, got %T", m.blocks[2])
	}
}

func TestBangCommand_BusyIsUnavailable(t *testing.T) {
	m := makeTestModel()
	m.busy = true

	cmd := m.handleBangCommand("!git status")

	if cmd != nil {
		t.Fatal("busy ! should not start a command")
	}
	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "while a turn is running") {
		t.Fatalf("unexpected error: %v", eb.err)
	}
}

func TestSlashPicker_ShowsAllCommandsThenFilters(t *testing.T) {
	withSlashCommands(t, []slashCommand{
		{"/help", "show this list"},
		{"/resume", "resume a session"},
	})
	m := makeTestModel()

	m.input.SetValue("/")
	m.updateSlashPicker()
	if !m.picker.visible {
		t.Fatal("expected picker to be visible")
	}
	if len(m.picker.matches) != 2 {
		t.Fatalf("expected all commands, got %d", len(m.picker.matches))
	}

	m.input.SetValue("/h")
	m.updateSlashPicker()
	if !m.picker.visible {
		t.Fatal("expected picker to stay visible")
	}
	if len(m.picker.matches) != 1 || m.picker.matches[0].cmd != "/help" {
		t.Fatalf("expected /help match, got %+v", m.picker.matches)
	}
}

func TestSlashPicker_ArrowKeysCycleSelection(t *testing.T) {
	withSlashCommands(t, []slashCommand{
		{"/help", "show this list"},
		{"/resume", "resume a session"},
	})
	m := makeTestModel()
	m.input.SetValue("/")
	m.updateSlashPicker()

	m.Update(keyPress(tea.KeyUp))
	if got := m.picker.matches[m.picker.selected].cmd; got != "/resume" {
		t.Fatalf("expected up to wrap to /resume, got %s", got)
	}
	m.Update(keyPress(tea.KeyDown))
	if got := m.picker.matches[m.picker.selected].cmd; got != "/help" {
		t.Fatalf("expected down to wrap to /help, got %s", got)
	}
}

func TestSlashPicker_TabAcceptsHighlightedCommand(t *testing.T) {
	withSlashCommands(t, []slashCommand{
		{"/help", "show this list"},
		{"/resume", "resume a session"},
	})
	m := makeTestModel()
	m.input.SetValue("/")
	m.updateSlashPicker()
	m.Update(keyPress(tea.KeyDown))
	m.Update(keyPress(tea.KeyTab))

	if got := m.input.Value(); got != "/resume" {
		t.Fatalf("expected accepted command in input, got %q", got)
	}
	if m.picker.visible {
		t.Fatal("expected picker to hide after accepting a command")
	}
}

func TestSlashPicker_EnterAcceptsIncompleteCommand(t *testing.T) {
	m := makeTestModel()
	m.input.SetValue("/h")
	m.updateSlashPicker()
	m.Update(keyPress(tea.KeyEnter))

	if got := m.input.Value(); got != "/help" {
		t.Fatalf("expected enter to complete /help, got %q", got)
	}
	if len(m.blocks) != 0 {
		t.Fatalf("expected command not to execute until submitted, got %d blocks", len(m.blocks))
	}
}

func TestSlashPicker_EnterSubmitsExactCommand(t *testing.T) {
	m := makeTestModel()
	m.input.SetValue("/help")
	m.updateSlashPicker()
	m.Update(keyPress(tea.KeyEnter))

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	if _, ok := m.blocks[0].(helpBlock); !ok {
		t.Fatalf("expected helpBlock, got %T", m.blocks[0])
	}
	if m.picker.visible {
		t.Fatal("expected picker to hide after command submission")
	}
}

func TestSlashPicker_EscapeDismissesUntilQueryChanges(t *testing.T) {
	m := makeTestModel()
	m.input.SetValue("/")
	m.updateSlashPicker()
	m.Update(keyPress(tea.KeyEsc))
	if m.picker.visible {
		t.Fatal("expected picker to hide after escape")
	}

	m.updateSlashPicker()
	if m.picker.visible {
		t.Fatal("expected picker to stay hidden for dismissed query")
	}

	m.input.SetValue("/h")
	m.updateSlashPicker()
	if !m.picker.visible {
		t.Fatal("expected picker to reappear when query changes")
	}
}

func TestSlashPicker_HidesWhenInputIsNotSlashCommand(t *testing.T) {
	m := makeTestModel()
	m.input.SetValue("/")
	m.updateSlashPicker()
	if !m.picker.visible {
		t.Fatal("expected picker to be visible")
	}

	m.input.SetValue("hello")
	m.updateSlashPicker()
	if m.picker.visible {
		t.Fatal("expected picker to hide for non-slash input")
	}

	m.input.SetValue(" /help")
	m.updateSlashPicker()
	if m.picker.visible {
		t.Fatal("expected picker to hide when slash is not the first input character")
	}
}

func TestSlashPicker_HideRelayoutsViewport(t *testing.T) {
	m := makeTestModel()
	m.input.SetValue("/")
	m.updateSlashPicker()
	withPicker := m.viewport.Height()

	m.input.SetValue("hello")
	m.updateSlashPicker()
	withoutPicker := m.viewport.Height()

	if withoutPicker <= withPicker {
		t.Fatalf("expected viewport to reclaim picker height, with=%d without=%d", withPicker, withoutPicker)
	}
}

func TestSlashPicker_RenderShowsCommandDescriptions(t *testing.T) {
	picker := commandPicker{
		visible: true,
		matches: []slashCommand{
			{"/help", "show this list"},
		},
	}
	out := plain(renderSlashPicker(80, picker))
	for _, want := range []string{"→ help", "show this list", "(1/1)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("picker missing %q: %s", want, out)
		}
	}
	for _, unwanted := range []string{"slash commands", "/help"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("picker should not include %q: %s", unwanted, out)
		}
	}
}

func TestSlashPicker_RenderFitsNarrowWidth(t *testing.T) {
	width := 16
	picker := commandPicker{
		visible: true,
		matches: []slashCommand{
			{"/very-long-command-name", "a long command description"},
			{"/help", "show this list"},
		},
	}
	out := plain(renderSlashPicker(width, picker))
	for _, line := range strings.Split(out, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line exceeds width %d: got %d line %q\n%s", width, got, line, out)
		}
	}
}

func TestSlashPicker_RendersBelowInput(t *testing.T) {
	m := makeTestModel()
	m.input.SetValue("/")
	m.updateSlashPicker()

	out := plain(m.View().Content)
	inputIndex := strings.Index(out, "/")
	pickerIndex := strings.Index(out, "→ clear")
	if inputIndex == -1 || pickerIndex == -1 {
		t.Fatalf("expected input and picker in view: %s", out)
	}
	if pickerIndex < inputIndex {
		t.Fatalf("expected picker below input: input=%d picker=%d\n%s", inputIndex, pickerIndex, out)
	}
}

func withSlashCommands(t *testing.T, commands []slashCommand) {
	t.Helper()
	oldBase := baseSlashCommands
	oldMemory := memorySlashCommand
	baseSlashCommands = commands
	memorySlashCommand = slashCommand{}
	t.Cleanup(func() {
		baseSlashCommands = oldBase
		memorySlashCommand = oldMemory
	})
}

func keyPress(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

type tuiEchoTool struct{}

func (tuiEchoTool) Name() string { return "bash" }
func (tuiEchoTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "bash", Description: "bash", InputSchema: map[string]any{"type": "object"}}
}
func (tuiEchoTool) Run(_ context.Context, in map[string]any) (string, error) {
	if s, ok := in["command"].(string); ok {
		return s, nil
	}
	return "", nil
}
