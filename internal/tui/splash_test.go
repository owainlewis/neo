package tui

import (
	"strings"
	"testing"
)

func TestSplashBlock_RendersWordmarkTaglineAndMetadata(t *testing.T) {
	b := splashBlock{
		version: "v0.2.0",
		model:   "claude-sonnet-4-6",
		cwd:     "~/Code/neo",
		branch:  "main",
	}
	out := plain(b.render(80, nil))

	for _, want := range []string{
		"NEO", "a coding agent",
		"v0.2.0", "claude-sonnet-4-6", "main", "~/Code/neo",
		"/help",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("splash missing %q:\n%s", want, out)
		}
	}
	// Inline `·` separator between metadata values.
	if !strings.Contains(out, "·") {
		t.Errorf("expected `·` separator in metadata row, got:\n%s", out)
	}
}

func TestSplashBlock_RendersGradientBar(t *testing.T) {
	b := splashBlock{version: "dev", model: "m", cwd: "/tmp"}
	out := plain(b.render(80, nil))
	// Five lines of █ — one per gradient stop. Count occurrences of the
	// block character (the bar) to confirm the lockup is the expected shape.
	got := strings.Count(out, "█")
	if got != len(gradient) {
		t.Fatalf("expected %d gradient bar chars, got %d in:\n%s", len(gradient), got, out)
	}
}

func TestSplashBlock_OmitsBranchWhenAbsent(t *testing.T) {
	b := splashBlock{version: "dev", model: "m", cwd: "/tmp"}
	out := plain(b.render(80, nil))
	// "main" shouldn't appear when no branch was supplied.
	if strings.Contains(out, "main") {
		t.Fatalf("expected no branch token in output, got:\n%s", out)
	}
}

func TestSplashBlock_OmitsBranchWhenNoGit(t *testing.T) {
	b := splashBlock{version: "dev", model: "m", cwd: "/tmp", branch: "no-git"}
	out := plain(b.render(80, nil))
	if strings.Contains(out, "no-git") {
		t.Fatalf("expected no-git sentinel to be suppressed, got:\n%s", out)
	}
}
