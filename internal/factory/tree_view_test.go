package factory

import (
	"strings"
	"testing"
	"time"
)

func TestRenderTreeShape(t *testing.T) {
	nodes := []NodeView{
		{ID: 1, Parent: 0, Step: "agent", Kind: "agent", Task: "work the backlog", Elapsed: 3*time.Minute + 12*time.Second, LastLine: "agent: review PR #34"},
		{ID: 2, Parent: 1, Step: "agent", Kind: "agent", Task: "#12 invite teammate", Elapsed: 2 * time.Minute, LastLine: "bash: just test"},
		{ID: 3, Parent: 2, Step: "agent", Kind: "agent", Task: "PR #34", Done: true, Elapsed: 31 * time.Second},
		{ID: 4, Parent: 1, Step: "checks", Kind: "script", Task: "34", Done: true, Err: "CHECKS FAILING", Elapsed: 2 * time.Second},
	}
	out := RenderTree(nodes)

	for _, want := range []string{
		"● agent",
		"├─ ● agent",
		"└─ ✓ agent",
		"└─ ✗ checks",
		"bash: just test",
		"3m12s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("frame missing %q:\n%s", want, out)
		}
	}
	// Done nodes don't show a status line; live ones do.
	if strings.Count(out, "agent: review PR #34") != 1 {
		t.Errorf("live root should show its last line once:\n%s", out)
	}
}

func TestRenderTreeEmpty(t *testing.T) {
	if out := RenderTree(nil); out != "" {
		t.Fatalf("empty tree should render empty, got %q", out)
	}
}

func TestClip(t *testing.T) {
	if got := clip("one\ntwo", 80); got != "one" {
		t.Errorf("clip multiline = %q", got)
	}
	if got := clip("aaaaaa", 4); got != "aaa…" {
		t.Errorf("clip long = %q", got)
	}
	if got := clip("héllo wörld", 6); got != "héllo…" {
		t.Errorf("clip multibyte = %q", got)
	}
}
