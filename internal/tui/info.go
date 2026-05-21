package tui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"charm.land/glamour/v2"

	"github.com/owainlewis/neo/internal/config"
)

// helpBlock renders the slash command reference.
type helpBlock struct{}

var slashCommands = []struct {
	cmd  string
	desc string
}{
	{"/run <flow> [task]", "start a flow (see /flows)"},
	{"/flows", "list available flows + their steps"},
	{"/cancel", "cancel the running flow"},
	{"/help", "show this list"},
}

func (helpBlock) render(width int, _ *glamour.TermRenderer) string {
	var sb strings.Builder
	sb.WriteString(styAccent.Render("slash commands") + "\n")
	for _, c := range slashCommands {
		sb.WriteString(fmt.Sprintf("  %s  %s\n",
			styTool.Render(padRight(c.cmd, 22)),
			styMuted.Render(c.desc)))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// flowsBlock lists configured flows with a step-resolution health check
// per flow so the user can see at a glance which ones are runnable.
type flowsBlock struct {
	source   string // "neo.yaml" / "~/.neo/config.yaml" / "embedded"
	entries  []flowEntry
	noFlows  bool
}

type flowEntry struct {
	name    string
	steps   []string
	round   int // max_rounds
	missing []string // step names that don't resolve because the file isn't found anywhere
	broken  []brokenStep // step names whose file exists but failed to parse / read
}

type brokenStep struct {
	name string
	err  string // short error description (e.g. "frontmatter: ...")
}

func buildFlowsBlock(cfg *config.Config) flowsBlock {
	if cfg == nil {
		return flowsBlock{source: "(no config)", noFlows: true}
	}
	if len(cfg.Flows) == 0 {
		return flowsBlock{source: cfg.Source(), noFlows: true}
	}
	names := cfg.FlowNames()
	sort.Strings(names) // FlowNames already sorts, but defensive
	out := flowsBlock{source: cfg.Source()}
	for _, n := range names {
		f := cfg.Flows[n]
		entry := flowEntry{name: n, steps: f.Steps, round: f.MaxRounds}
		for _, step := range f.Steps {
			if _, err := cfg.ResolveStep(step); err != nil {
				// Distinguish "file doesn't exist anywhere" (missing) from
				// "file exists but failed to parse / read" (broken). The
				// two have different fixes and sending a user toward "add
				// the file" when the file is actually present + malformed
				// is the wrong path.
				var notFound *config.StepNotFoundError
				if errors.As(err, &notFound) {
					entry.missing = append(entry.missing, step)
				} else {
					entry.broken = append(entry.broken, brokenStep{name: step, err: err.Error()})
				}
			}
		}
		out.entries = append(out.entries, entry)
	}
	return out
}

func (b flowsBlock) render(width int, _ *glamour.TermRenderer) string {
	var sb strings.Builder
	sb.WriteString(styAccent.Render("flows") + styDim.Render(" — from "+b.source) + "\n")

	if b.noFlows {
		sb.WriteString(styMuted.Render("  (no flows defined)") + "\n")
		sb.WriteString(styDim.Render("  define flows under `flows:` in your neo.yaml") + "\n")
		return strings.TrimRight(sb.String(), "\n")
	}

	// Column 1 is the name; pad to the widest.
	nameW := 0
	for _, e := range b.entries {
		if len(e.name) > nameW {
			nameW = len(e.name)
		}
	}

	for _, e := range b.entries {
		var glyph string
		if len(e.missing) == 0 && len(e.broken) == 0 {
			glyph = styOK.Render("✓")
		} else {
			glyph = styErr.Render("✗")
		}
		stepsLine := strings.Join(e.steps, " → ")
		round := ""
		if e.round > 1 {
			round = styDim.Render(fmt.Sprintf("  (max %d rounds)", e.round))
		}
		sb.WriteString(fmt.Sprintf("  %s %s  %s%s\n",
			glyph,
			padRight(e.name, nameW+2),
			styMuted.Render(stepsLine),
			round))
		if len(e.missing) > 0 {
			sb.WriteString(fmt.Sprintf("      %s\n",
				styErr.Render("missing step(s): "+strings.Join(e.missing, ", "))))
		}
		for _, b := range e.broken {
			sb.WriteString(fmt.Sprintf("      %s\n",
				styErr.Render(fmt.Sprintf("broken step %q: %s", b.name, b.err))))
		}
	}
	sb.WriteString("\n" + styDim.Render("  run with: /run <name> [task]"))
	return strings.TrimRight(sb.String(), "\n")
}
