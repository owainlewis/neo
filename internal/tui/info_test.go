package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
)

func TestHelpBlock_ListsAllSlashCommands(t *testing.T) {
	out := plain(helpBlock{}.render(80, nil))
	for _, c := range []string{"/run", "/flows", "/cancel", "/help"} {
		if !strings.Contains(out, c) {
			t.Errorf("/help missing command %q: %s", c, out)
		}
	}
}

func TestFlowsBlock_EmptyConfigHandled(t *testing.T) {
	b := buildFlowsBlock(nil)
	out := plain(b.render(80, nil))
	if !strings.Contains(out, "no config") && !strings.Contains(out, "no flows defined") {
		t.Fatalf("expected empty-state message, got:\n%s", out)
	}
}

func TestFlowsBlock_ListsHealthyFlowFromEmbedded(t *testing.T) {
	// Embedded defaults ship the `code` flow with healthy steps.
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	out := plain(buildFlowsBlock(cfg).render(80, nil))
	if !strings.Contains(out, "code") {
		t.Fatalf("expected 'code' flow, got:\n%s", out)
	}
	if !strings.Contains(out, "write → review") {
		t.Fatalf("expected steps separator, got:\n%s", out)
	}
	if !strings.Contains(out, "✓") {
		t.Fatalf("expected health check ✓, got:\n%s", out)
	}
	if strings.Contains(out, "✗") {
		t.Fatalf("embedded code flow should be healthy, got ✗:\n%s", out)
	}
}

func TestFlowsBlock_MarksBrokenFlowWithMissingSteps(t *testing.T) {
	cfg := &config.Config{
		Flows: map[string]config.FlowConfig{
			"broken": {Steps: []string{"definitely-not-a-real-step"}},
		},
	}
	out := plain(buildFlowsBlock(cfg).render(80, nil))
	if !strings.Contains(out, "✗") {
		t.Fatalf("expected ✗ for broken flow, got:\n%s", out)
	}
	if !strings.Contains(out, "missing step") {
		t.Fatalf("expected 'missing step' diagnostic, got:\n%s", out)
	}
}

// Regression for the #32 P2 feedback: a step file that exists but fails
// to parse (e.g. malformed frontmatter) used to be reported as "missing",
// sending the user toward the wrong fix. Should now be labelled "broken".
func TestFlowsBlock_DistinguishesBrokenFromMissing(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)

	flowsDir := filepath.Join(dir, "flows")
	if err := os.MkdirAll(flowsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// "broken-step" exists but has bad frontmatter.
	if err := os.WriteFile(filepath.Join(flowsDir, "broken-step.md"),
		[]byte("---\nbad yaml: [unclosed\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Build a config with two failure modes: broken-step (parse error) and
	// missing-step (not found anywhere).
	if err := os.WriteFile(filepath.Join(dir, "neo.yaml"),
		[]byte("flows:\n  scratch:\n    steps: [broken-step, missing-step]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	out := plain(buildFlowsBlock(cfg).render(80, nil))

	if !strings.Contains(out, "missing step(s): missing-step") {
		t.Fatalf("expected 'missing step(s)' diagnostic for absent file, got:\n%s", out)
	}
	if !strings.Contains(out, `broken step "broken-step"`) {
		t.Fatalf("expected 'broken step' diagnostic for parse-error file, got:\n%s", out)
	}
	if strings.Contains(out, "missing step(s): broken-step") {
		t.Fatalf("broken file should not be labelled missing, got:\n%s", out)
	}
}

func TestSlashCommand_FlowsListsAvailable(t *testing.T) {
	m := makeTestModel(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	m.wf.Config = cfg
	m.handleSlashCommand("/flows")

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	if _, ok := m.blocks[0].(flowsBlock); !ok {
		t.Fatalf("expected flowsBlock, got %T", m.blocks[0])
	}
}

func TestSlashCommand_HelpAppendsHelpBlock(t *testing.T) {
	m := makeTestModel(t)
	m.handleSlashCommand("/help")
	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	if _, ok := m.blocks[0].(helpBlock); !ok {
		t.Fatalf("expected helpBlock, got %T", m.blocks[0])
	}
}

func TestSlashCommand_UnknownSuggestsHelp(t *testing.T) {
	m := makeTestModel(t)
	m.handleSlashCommand("/wat")
	eb, ok := m.blocks[0].(errorBlock)
	if !ok {
		t.Fatalf("expected errorBlock, got %T", m.blocks[0])
	}
	if !strings.Contains(eb.err.Error(), "/help") {
		t.Fatalf("error should suggest /help, got %v", eb.err)
	}
}
