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
		{name: "later command", tool: "bash", args: map[string]any{"command": "cd repo && git status"}, want: true},
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

func TestMatcherShellCommands(t *testing.T) {
	matcher := New([]string{"git push", "rm -rf"})
	tests := []struct {
		command string
		want    bool
	}{
		{"cd sub && git push", true},
		{"git  push", true},
		{"git\tpush origin main", true},
		{"VAR=1 git push", true},
		{"_VAR2='two words' OTHER= git push", true},
		{`sh -c "git push"`, true},
		{`bash -c 'cd sub && VAR=1 git  push'`, true},
		{`sh -c 'sh -c "git push"'`, true},
		{"echo a; rm -rf b", true},
		{"false || git push", true},
		{"echo a | git push", true},
		{"echo a\ngit push", true},
		{"echo a & git push", true},
		{"git pushy", false},
		{"Git push", false},
		{"echo git push", false},
		{`echo "a; rm -rf b"`, false},
		{`echo 'a && git push'`, false},
		{`echo a\; rm -rf b`, false},
		{`VAR="a; b" git push`, true},
		{`VAR="git push"`, false},
		{"1VAR=1 git push", false},
		{"VAR-NAME=1 git push", false},
		{"=1 git push", false},
		{`sh -c 'echo git push'`, false},
		{`sh -c 'echo ok' git push`, false},
		{"", false},
		{" ; && \n", false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			if got := matcher.Requires("bash", map[string]any{"command": tt.command}); got != tt.want {
				t.Fatalf("Requires(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
	if matcher.Requires("read_file", map[string]any{"command": "git push"}) {
		t.Fatal("command matching must only apply to bash")
	}
}
