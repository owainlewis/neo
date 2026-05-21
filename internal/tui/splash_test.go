package tui

import (
	"strings"
	"testing"
)

func TestSplashBlock_RendersWordmarkAndMetadata(t *testing.T) {
	b := splashBlock{
		version: "v0.2.0",
		model:   "claude-sonnet-4-6",
		cwd:     "~/Code/neo",
		branch:  "main",
	}
	out := plain(b.render(80, nil))

	// Wordmark "n e o" appears inside the rounded box.
	if !strings.Contains(out, "n e o") {
		t.Fatalf("expected wordmark 'n e o' in splash, got:\n%s", out)
	}
	// Rounded border corner — sanity check the box is present.
	if !strings.Contains(out, "╭") || !strings.Contains(out, "╯") {
		t.Fatalf("expected rounded box corners in splash, got:\n%s", out)
	}
	// All metadata + the /help hint are present.
	for _, want := range []string{"v0.2.0", "claude-sonnet-4-6", "~/Code/neo", "main", "/help"} {
		if !strings.Contains(out, want) {
			t.Errorf("splash missing %q:\n%s", want, out)
		}
	}
	// Inline `·` separator between metadata values.
	if !strings.Contains(out, "·") {
		t.Errorf("expected inline `·` separator in metadata row, got:\n%s", out)
	}
}

func TestSplashBlock_OmitsBranchWhenAbsent(t *testing.T) {
	b := splashBlock{version: "dev", model: "m", cwd: "/tmp"}
	out := plain(b.render(80, nil))
	// Branch shouldn't appear when it wasn't supplied.
	if strings.Contains(out, "main") || strings.Contains(out, "no-git") {
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
