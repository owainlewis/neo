package tui

import (
	"fmt"
	"strings"

	"charm.land/glamour/v2"

	"github.com/owainlewis/neo/internal/promptcmd"
)

// helpBlock renders the available slash commands and key bindings.
type helpBlock struct{ commands []slashCommand }

type slashCommand struct {
	cmd  string
	desc string
}

var baseSlashCommands = []slashCommand{
	{"/clear", "clear the current transcript"},
	{"/help", "show this list"},
	{"/model", "select the active model"},
	{"/permissions", "select permission mode"},
	{"/sessions", "resume a saved session"},
	{"/tokens", "show token usage"},
	{"/tools", "list available tools"},
}

var memorySlashCommand = slashCommand{"/memory", "append a project memory entry"}

var keyBindings = []struct {
	key  string
	desc string
}{
	{"↩", "send"},
	{"⌥↩", "newline"},
	{"!cmd", "run shell command"},
	{"tab", "toggle workflow panel (accepts picker selection first)"},
	{"ctrl+o", "expand/collapse latest truncated tool output"},
	{"esc", "cancel the current turn"},
	{"ctrl+l", "clear the screen"},
	{"ctrl+c", "quit"},
}

func (h helpBlock) render(width int, _ *glamour.TermRenderer) string {
	var sb strings.Builder
	sb.WriteString(styAccent.Render("slash commands") + "\n")
	for _, c := range h.commands {
		sb.WriteString(fmt.Sprintf("  %s  %s\n",
			styTool.Render(padRight(c.cmd, 10)),
			styMuted.Render(c.desc)))
	}
	sb.WriteString("\n" + styAccent.Render("keys") + "\n")
	for _, k := range keyBindings {
		sb.WriteString(fmt.Sprintf("  %s  %s\n",
			styTool.Render(padRight(k.key, 10)),
			styMuted.Render(k.desc)))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m *model) slashCommands() []slashCommand {
	commands := append([]slashCommand(nil), baseSlashCommands...)
	if m.memoryEnabled {
		commands = append(commands, memorySlashCommand)
	}
	used := map[string]bool{}
	for _, c := range commands {
		used[c.cmd] = true
	}
	for _, c := range m.promptCommands {
		cmd := "/" + c.Name
		if used[cmd] {
			continue
		}
		used[cmd] = true
		commands = append(commands, slashCommand{cmd: cmd, desc: c.Description})
	}
	return commands
}

func (m *model) promptCommand(cmd string) (promptcmd.Command, bool) {
	if builtinSlashCommand(cmd) {
		return promptcmd.Command{}, false
	}
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cmd)), "/")
	for _, c := range m.promptCommands {
		if c.Name == name {
			return c, true
		}
	}
	return promptcmd.Command{}, false
}

func builtinSlashCommand(cmd string) bool {
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	for _, c := range baseSlashCommands {
		if c.cmd == cmd {
			return true
		}
	}
	return memorySlashCommand.cmd == cmd
}
