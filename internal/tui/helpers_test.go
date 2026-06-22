package tui

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	"github.com/charmbracelet/x/ansi"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/tools"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain strips ANSI escape codes so tests can assert on rendered text content.
func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// firstLine returns the first line of s, for asserting on header ordering.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// makeTestModel builds a minimal model for slash-command and state-transition
// tests without going through newModel (which probes the terminal). Only the
// fields exercised by tests are populated.
func makeTestModel() *model {
	root, _ := os.MkdirTemp("", "neo-tui-memory-*")
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
		projectRoot:    filepath.Clean(root),
		memoryEnabled:  true,
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		// ASCII regression cases (1 cell per char).
		{"ascii shorter than n", "hello", 10, "hello"},
		{"ascii exact length", "hello", 5, "hello"},
		{"ascii one over", "hello!", 5, "hell…"},
		{"ascii cut", "hello world", 8, "hello w…"},
		// Very small widths.
		{"n=0 empty", "", 0, ""},
		{"n=0 nonempty", "abc", 0, "…"},
		{"n=1 fits", "a", 1, "a"},
		{"n=1 cut", "abc", 1, "…"},
		{"n=2 cut", "abc", 2, "a…"},
		{"n=2 fits", "ab", 2, "ab"},
		// CJK (2 cells per char).
		{"cjk fits exactly", "日本語", 6, "日本語"},
		{"cjk cut", "日本語のテキスト", 5, "日本…"},
		{"cjk cut odd width", "日本語", 3, "日…"},
		{"cjk wide char cannot half-fit", "日本語", 2, "…"},
		// Accented characters (1 cell, multi-byte).
		{"accented fits", "café", 4, "café"},
		{"accented cut", "crème brûlée", 6, "crème…"},
		// Emoji (2 cells per char).
		{"emoji fits exactly", "🎉🎊🎈", 6, "🎉🎊🎈"},
		{"emoji cut", "🎉🎊🎈🎁", 5, "🎉🎊…"},
		{"emoji n=1", "🎉🎊", 1, "…"},
		// Mixed narrow and wide content.
		{"mixed cut", "a日b語c", 5, "a日b…"},
		// Combining mark stays attached to its base (grapheme boundary):
		// "e" + U+0301 is one 1-cell grapheme, never split from its accent.
		{"combining mark kept", "e\u0301xyz", 2, "e\u0301…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.s, tt.n)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("truncate(%q, %d) = %q is not valid UTF-8", tt.s, tt.n, got)
			}
			if w := ansi.StringWidth(got); tt.n > 0 && w > tt.n {
				t.Errorf("truncate(%q, %d) = %q is %d cells wide, want at most %d", tt.s, tt.n, got, w, tt.n)
			}
		})
	}
}

// TestTruncateANSIAware verifies styled lines (as built by the pickers) are
// measured by visible cells, not escape-sequence bytes, and never exceed n.
func TestTruncateANSIAware(t *testing.T) {
	styled := "\x1b[1mhello\x1b[0m \x1b[2m日本語\x1b[0m"
	if got := truncate(styled, 20); got != styled {
		t.Errorf("truncate should not cut a string of 12 visible cells at width 20, got %q", got)
	}
	// At n=8 only 7 cells remain beside the tail, so 日 (2 cells) cannot fit.
	got := truncate(styled, 8)
	if w := ansi.StringWidth(got); w > 8 {
		t.Errorf("truncate(styled, 8) is %d cells wide, want at most 8", w)
	}
	if p := plain(got); p != "hello …" {
		t.Errorf("truncate(styled, 8) visible text = %q, want %q", p, "hello …")
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncate(styled, 8) = %q is not valid UTF-8", got)
	}
	// At n=9 the first wide char fits beside the tail.
	if p := plain(truncate(styled, 9)); p != "hello 日…" {
		t.Errorf("truncate(styled, 9) visible text = %q, want %q", p, "hello 日…")
	}
}

func TestTruncateAlwaysValidUTF8(t *testing.T) {
	inputs := []string{"日本語のテキストです", "crème brûlée à la mode", "🎉🎊🎈🎁🎀", "héllo wörld", "abc日本🎉def", "e\u0301e\u0301e\u0301"}
	for _, s := range inputs {
		for n := 0; n <= ansi.StringWidth(s)+2; n++ {
			got := truncate(s, n)
			if !utf8.ValidString(got) {
				t.Errorf("truncate(%q, %d) = %q is not valid UTF-8", s, n, got)
			}
			if w := ansi.StringWidth(got); n > 0 && w > n {
				t.Errorf("truncate(%q, %d) = %q is %d cells wide, want at most %d", s, n, got, w, n)
			}
		}
	}
}

func TestPadRight(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		// ASCII regression cases.
		{"ascii pad", "ab", 5, "ab   "},
		{"ascii exact", "abcde", 5, "abcde"},
		{"ascii wider", "abcdef", 5, "abcdef"},
		{"empty", "", 3, "   "},
		{"n=0", "ab", 0, "ab"},
		// Wide chars: pad by display cells, not bytes or runes.
		{"cjk already wider", "日本語", 5, "日本語"},
		{"cjk exact", "日本語", 6, "日本語"},
		{"cjk pad", "日本語", 8, "日本語  "},
		{"accented pad", "café", 6, "café  "},
		{"emoji exact", "🎉🎊", 4, "🎉🎊"},
		{"emoji pad", "🎉🎊", 6, "🎉🎊  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := padRight(tt.s, tt.n)
			if got != tt.want {
				t.Errorf("padRight(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
			if w := ansi.StringWidth(got); w < tt.n {
				t.Errorf("padRight(%q, %d) = %q is %d cells wide, want at least %d", tt.s, tt.n, got, w, tt.n)
			}
		})
	}
}
