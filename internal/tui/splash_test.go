package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestSplashBlock_RendersProductMessageAndProjectContext(t *testing.T) {
	b := splashBlock{
		version: "v0.2.0",
		model:   "claude-opus-4-8",
		cwd:     "~/Code/neo",
		branch:  "main",
	}
	out := plain(b.render(80, nil))

	for _, want := range []string{
		"NEO", "workflow-first coding agent", "Plans the work, executes it, and verifies the result.",
		"~/Code/neo · main", "claude-opus-4-8 · v0.2.0", "Try:", "@ add files", "/ commands",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("splash missing %q:\n%s", want, out)
		}
	}
}

func TestSplashBlock_OmitsMissingGitContext(t *testing.T) {
	for _, branch := range []string{"", "no-git"} {
		b := splashBlock{version: "dev", model: "m", cwd: "/tmp", branch: branch}
		out := plain(b.render(80, nil))
		if strings.Contains(out, "no-git") {
			t.Fatalf("no-git sentinel should be suppressed, got:\n%s", out)
		}
		if !strings.Contains(out, "/tmp") {
			t.Fatalf("cwd missing without git context, got:\n%s", out)
		}
	}
}

func TestSplashBlock_RespectsWidth(t *testing.T) {
	const width = 32
	b := splashBlock{
		version: "v0.2.0",
		model:   "provider/a-deliberately-long-model-name",
		cwd:     "~/Code/a-deliberately-long-repository-name",
		branch:  "feature/a-deliberately-long-branch",
	}
	for _, line := range strings.Split(b.render(width, nil), "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line width = %d, want <= %d: %q", got, width, plain(line))
		}
	}
}
