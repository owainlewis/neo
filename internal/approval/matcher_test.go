package approval

import "testing"

func TestMatcherRequiresToolOrBashPrefix(t *testing.T) {
	matcher := New([]string{"write_file", "git", "rm -rf"})

	tests := []struct {
		name string
		tool string
		args map[string]any
		want bool
	}{
		{name: "exact tool", tool: "write_file", want: true},
		{name: "other tool", tool: "read_file", want: false},
		{name: "bash prefix", tool: "bash", args: map[string]any{"command": "git status"}, want: true},
		{name: "leading whitespace", tool: "bash", args: map[string]any{"command": "\t rm -rf build"}, want: true},
		{name: "exact command", tool: "bash", args: map[string]any{"command": "git"}, want: true},
		{name: "token boundary", tool: "bash", args: map[string]any{"command": "github status"}, want: false},
		{name: "case sensitive", tool: "bash", args: map[string]any{"command": "Git status"}, want: false},
		{name: "not at command start", tool: "bash", args: map[string]any{"command": "cd repo && git status"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matcher.Requires(tt.tool, tt.args); got != tt.want {
				t.Fatalf("Requires(%q, %#v) = %v, want %v", tt.tool, tt.args, got, tt.want)
			}
		})
	}
}

func TestMatcherCopiesRules(t *testing.T) {
	rules := []string{"git"}
	matcher := New(rules)
	rules[0] = "rm"

	if !matcher.Requires("bash", map[string]any{"command": "git status"}) {
		t.Fatal("matcher rules changed with caller slice")
	}
}
