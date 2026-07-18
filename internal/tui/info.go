package tui

import (
	"fmt"
	"strings"

	"charm.land/glamour/v2"

	"github.com/owainlewis/neo/internal/skills"
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
	{"↩ (working)", "steer after the current tool boundary"},
	{"ctrl+↩", "queue one follow-up after the current turn"},
	{"⌥↩", "newline"},
	{"!cmd", "run shell command"},
	{"wheel/pgup/pgdn", "scroll transcript"},
	{"shift+drag", "select terminal text"},
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
	if memorySlashCommand.cmd != "" {
		used[memorySlashCommand.cmd] = true
	}
	for _, s := range m.skills {
		cmd := "/" + s.Name
		if used[cmd] {
			continue
		}
		used[cmd] = true
		commands = append(commands, slashCommand{cmd: cmd, desc: skillDescription(s)})
	}
	return commands
}

func (m *model) slashSkill(cmd string) (skills.Skill, bool) {
	if builtinSlashCommand(cmd) {
		return skills.Skill{}, false
	}
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cmd)), "/")
	for _, s := range m.skills {
		if s.Name == name {
			return s, true
		}
	}
	return skills.Skill{}, false
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

func skillDescription(s skills.Skill) string {
	if strings.TrimSpace(s.Description) != "" {
		return s.Description
	}
	return "apply skill"
}
