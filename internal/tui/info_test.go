package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/agent"
)

func TestHelpBlock_ListsHelpCommandAndKeys(t *testing.T) {
	out := plain(helpBlock{}.render(80, nil))
	for _, want := range []string{"/help", "send", "newline", "quit"} {
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
		{"/permissions", "permissions: ask"},
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
	m.blocks = append(m.blocks, noticeBlock{text: "x"})
	m.handleSlashCommand("/clear")
	if len(m.blocks) != 0 {
		t.Fatalf("/clear left %d blocks", len(m.blocks))
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
	m := makeTestModel()
	reply := make(chan bool, 1)
	m.Update(approvalRequestMsg{
		req:   agent.ApprovalRequest{ToolName: "bash", Preview: "preview"},
		reply: reply,
	})
	if m.approval == nil {
		t.Fatal("expected pending approval")
	}
	m.Update(keyPress('y'))
	if got := <-reply; !got {
		t.Fatal("expected approval reply true")
	}
	if m.approval != nil {
		t.Fatal("expected approval to clear")
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
	old := slashCommands
	slashCommands = commands
	t.Cleanup(func() { slashCommands = old })
}

func keyPress(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}
