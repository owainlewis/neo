package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestSplashBlock_RendersCenteredWordmarkAndGuidance(t *testing.T) {
	out := plain((splashBlock{version: "v0.2.0"}).render(80, nil))

	for _, want := range []string{
		` _   _  _____   ___ `,
		`| \ | || ____| / _ \`,
		`|_| \_||_____| \___/`,
		"workflow-first coding agent · v0.2.0",
		"TIP: Use @ to add files or / for commands.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("splash missing %q:\n%s", want, out)
		}
	}
}

func TestSplashBlock_DoesNotRepeatFooterContext(t *testing.T) {
	out := plain((splashBlock{version: "v0.2.0"}).render(80, nil))
	for _, duplicate := range []string{"~/Code/neo", "main", "claude-opus"} {
		if strings.Contains(out, duplicate) {
			t.Fatalf("splash repeated footer context %q:\n%s", duplicate, out)
		}
	}
}

func TestCenterSplashLine(t *testing.T) {
	if got := centerSplashLine("NEO", 19); got != "        NEO" {
		t.Fatalf("centerSplashLine() = %q", got)
	}
}

func TestSplashBlock_UsesCompactFallbackAtNarrowWidths(t *testing.T) {
	const width = 16
	b := splashBlock{version: "a-deliberately-long-version-name"}
	out := b.render(width, nil)
	plainOut := plain(out)
	for _, want := range []string{"NEO", "@ add files", "/ commands"} {
		if !strings.Contains(plainOut, want) {
			t.Errorf("compact splash missing %q:\n%s", want, plainOut)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line width = %d, want <= %d: %q", got, width, plain(line))
		}
	}
}
