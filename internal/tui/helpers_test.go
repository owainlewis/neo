package tui

import (
	"context"
	"regexp"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/tools"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain strips ANSI escape codes so tests can assert on rendered text content.
func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// makeTestModel builds a minimal model for slash-command and state-transition
// tests without going through newModel (which probes the terminal). Only the
// fields exercised by tests are populated.
func makeTestModel() *model {
	ta := textarea.New()
	ta.Focus()
	ta.SetWidth(78)
	return &model{
		ctx:            context.Background(),
		width:          80,
		height:         24,
		ag:             agent.New(agent.Config{Model: "test", Provider: &llmtest.FakeProvider{}, Tools: tools.NewRegistry(tools.ReadFile{}), Policy: permission.New("ask", ".")}),
		input:          ta,
		viewport:       viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
		modelTag:       "test",
		cwd:            "~",
		branch:         "main",
		permissionMode: "ask",
	}
}
