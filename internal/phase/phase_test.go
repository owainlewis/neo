package phase

import (
	"strings"
	"testing"
)

func TestResolveKeepsDefaultsAndAddsConfiguredPhase(t *testing.T) {
	definitions, err := Resolve(map[string]Definition{
		"security": {Description: "Review security", Prompt: "Inspect trust boundaries."},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := []string{"design", "plan", "build", "review", "security"}
	if len(definitions) != len(want) {
		t.Fatalf("definitions = %d, want %d", len(definitions), len(want))
	}
	for i, name := range want {
		if definitions[i].Name != name {
			t.Fatalf("definition %d = %q, want %q", i, definitions[i].Name, name)
		}
	}
}

func TestResolveOverridesDefaultFields(t *testing.T) {
	definitions, err := Resolve(map[string]Definition{
		"review": {Prompt: "Use the project review policy."},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	review, ok := Find(definitions, "review")
	if !ok {
		t.Fatal("review phase missing")
	}
	if review.Prompt != "Use the project review policy." {
		t.Fatalf("review prompt = %q", review.Prompt)
	}
	if review.Description == "" {
		t.Fatal("default description was not retained")
	}
}

func TestResolveRejectsInvalidConfiguredPhase(t *testing.T) {
	for _, test := range []struct {
		name string
		defs map[string]Definition
	}{
		{name: "uppercase", defs: map[string]Definition{"Security": {Prompt: "x"}}},
		{name: "reserved", defs: map[string]Definition{"help": {Prompt: "x"}}},
		{name: "missing prompt", defs: map[string]Definition{"security": {Description: "x"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Resolve(test.defs); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestMatchRunRecognizesSingleAndOrderedPhases(t *testing.T) {
	definitions, _ := Resolve(nil)
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "Run the review phase", want: "review"},
		{input: "Please run design, plan and build phases for this feature", want: "design,plan,build"},
		{input: "run the build plan design phases", want: "build,plan,design"},
	} {
		selected := MatchRun(test.input, definitions)
		var names []string
		for _, definition := range selected {
			names = append(names, definition.Name)
		}
		if got := strings.Join(names, ","); got != test.want {
			t.Fatalf("MatchRun(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestMatchRunRejectsCasualOrAmbiguousMentions(t *testing.T) {
	definitions, _ := Resolve(nil)
	for _, input := range []string{
		"Tell me what the review phase does",
		"Run tests before the review phase",
		"Review this code",
	} {
		if got := MatchRun(input, definitions); len(got) != 0 {
			t.Fatalf("MatchRun(%q) = %+v, want none", input, got)
		}
	}
}

func TestExpandPreservesRequestAndOrdersPrompts(t *testing.T) {
	definitions, _ := Resolve(nil)
	design, _ := Find(definitions, "design")
	plan, _ := Find(definitions, "plan")
	got := Expand("add encrypted sessions", []Definition{design, plan})
	if !strings.Contains(got, "[named phases: design, plan]") || !strings.HasSuffix(got, "add encrypted sessions") {
		t.Fatalf("unexpected expansion:\n%s", got)
	}
	if strings.Index(got, "[phase: design]") > strings.Index(got, "[phase: plan]") {
		t.Fatalf("phase prompts out of order:\n%s", got)
	}
}
