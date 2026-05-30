package tui

import (
	"strings"
	"testing"
)

func TestSplashBlock_RendersWordmarkTaglineAndMetadataList(t *testing.T) {
	b := splashBlock{
		version: "v0.2.0",
		model:   "claude-opus-4-8",
		cwd:     "~/Code/neo",
		branch:  "main",
		tagline: "Ship it",
	}
	out := plain(b.render(80, nil))

	// Wordmark + tagline + hint.
	for _, want := range []string{"NEO", "Ship it", "/help"} {
		if !strings.Contains(out, want) {
			t.Errorf("splash missing %q:\n%s", want, out)
		}
	}
	// Labelled list — each label appears alongside its value.
	type pair struct{ label, value string }
	for _, p := range []pair{
		{"version", "v0.2.0"},
		{"model", "claude-opus-4-8"},
		{"branch", "main"},
		{"cwd", "~/Code/neo"},
	} {
		if !strings.Contains(out, p.label) || !strings.Contains(out, p.value) {
			t.Errorf("expected list row %q → %q, got:\n%s", p.label, p.value, out)
		}
	}
}

func TestSplashBlock_GradientMatchesContentHeight(t *testing.T) {
	// Full set: 3 header lines (wordmark, tagline, blank) + 4 list rows = 7.
	full := splashBlock{version: "v", model: "m", cwd: "/c", branch: "main"}
	if got := strings.Count(plain(full.render(80, nil)), "█"); got != 7 {
		t.Fatalf("expected 7 gradient bar chars with branch, got %d", got)
	}
	// Without branch: 3 header + 3 list rows = 6.
	short := splashBlock{version: "v", model: "m", cwd: "/c"}
	if got := strings.Count(plain(short.render(80, nil)), "█"); got != 6 {
		t.Fatalf("expected 6 gradient bar chars without branch, got %d", got)
	}
}

func TestSplashBlock_OmitsBranchRowWhenAbsent(t *testing.T) {
	b := splashBlock{version: "dev", model: "m", cwd: "/tmp"}
	out := plain(b.render(80, nil))
	if strings.Contains(out, "branch") {
		t.Fatalf("expected no branch row when branch is empty, got:\n%s", out)
	}
}

func TestSplashBlock_OmitsBranchRowWhenNoGit(t *testing.T) {
	b := splashBlock{version: "dev", model: "m", cwd: "/tmp", branch: "no-git"}
	out := plain(b.render(80, nil))
	if strings.Contains(out, "no-git") {
		t.Fatalf("no-git sentinel should be suppressed, got:\n%s", out)
	}
	if strings.Contains(out, "branch") {
		t.Fatalf("branch row should be omitted on no-git, got:\n%s", out)
	}
}

func TestTaglines_AllNonEmptyAndCapitalized(t *testing.T) {
	if len(taglines) == 0 {
		t.Fatal("expected at least one tagline")
	}
	for _, tl := range taglines {
		if tl == "" {
			t.Error("tagline must not be empty")
			continue
		}
		first := rune(tl[0])
		if first < 'A' || first > 'Z' {
			t.Errorf("tagline %q must start with a capital letter", tl)
		}
	}
}

func TestRandomTagline_ReturnsAMember(t *testing.T) {
	got := randomTagline()
	for _, tl := range taglines {
		if tl == got {
			return
		}
	}
	t.Fatalf("randomTagline returned %q which is not in taglines", got)
}

func TestGradientFor_PicksAcrossPalette(t *testing.T) {
	cases := []struct {
		n            int
		firstIsLight bool
		lastIsDark   bool
	}{
		{n: 1, firstIsLight: true, lastIsDark: false}, // single stop is light
		{n: 5, firstIsLight: true, lastIsDark: true},
		{n: 8, firstIsLight: true, lastIsDark: true},
	}
	for _, c := range cases {
		got := gradientFor(c.n)
		if len(got) != c.n {
			t.Errorf("gradientFor(%d) len = %d, want %d", c.n, len(got), c.n)
		}
		if got[0] != skyPalette[0] {
			t.Errorf("gradientFor(%d)[0] = %s, want palette start %s", c.n, got[0], skyPalette[0])
		}
		if c.lastIsDark && got[len(got)-1] != skyPalette[len(skyPalette)-1] {
			t.Errorf("gradientFor(%d) last = %s, want palette end %s", c.n, got[len(got)-1], skyPalette[len(skyPalette)-1])
		}
	}
}
