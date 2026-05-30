package tui

import (
	"fmt"
	"strings"

	"charm.land/glamour/v2"
)

// helpBlock renders the available slash commands and key bindings.
type helpBlock struct{}

var slashCommands = []struct {
	cmd  string
	desc string
}{
	{"/help", "show this list"},
}

var keyBindings = []struct {
	key  string
	desc string
}{
	{"↩", "send"},
	{"⌥↩", "newline"},
	{"esc", "cancel the current turn"},
	{"ctrl+l", "clear the screen"},
	{"ctrl+c", "quit"},
}

func (helpBlock) render(width int, _ *glamour.TermRenderer) string {
	var sb strings.Builder
	sb.WriteString(styAccent.Render("slash commands") + "\n")
	for _, c := range slashCommands {
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
