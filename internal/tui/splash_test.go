package tui

import (
	"strings"
	"testing"
)

func TestSplashBlock_RendersBannerAndMetadata(t *testing.T) {
	b := splashBlock{
		version: "v0.2.0",
		model:   "claude-sonnet-4-6",
		cwd:     "~/Code/neo",
		branch:  "main",
	}
	out := plain(b.render(80, nil))

	// Banner: a recognisable fragment from the first line.
	if !strings.Contains(out, "███") {
		t.Fatalf("expected banner glyphs in splash, got:\n%s", out)
	}
	for _, want := range []string{"v0.2.0", "claude-sonnet-4-6", "~/Code/neo", "main", "/help"} {
		if !strings.Contains(out, want) {
			t.Errorf("splash missing %q:\n%s", want, out)
		}
	}
}

func TestSplashBlock_OmitsBranchWhenAbsent(t *testing.T) {
	b := splashBlock{version: "dev", model: "m", cwd: "/tmp"}
	out := plain(b.render(80, nil))
	if strings.Contains(out, "branch") {
		t.Fatalf("expected no branch row when branch is empty, got:\n%s", out)
	}
}

func TestSplashBlock_OmitsBranchWhenNoGit(t *testing.T) {
	b := splashBlock{version: "dev", model: "m", cwd: "/tmp", branch: "no-git"}
	out := plain(b.render(80, nil))
	if strings.Contains(out, "no-git") {
		t.Fatalf("expected no-git sentinel to be suppressed, got:\n%s", out)
	}
}
