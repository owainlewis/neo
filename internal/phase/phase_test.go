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

func TestExpandInvocationPreservesRequest(t *testing.T) {
	definitions, _ := Resolve(nil)
	review, _ := Find(definitions, "review")
	got := ExpandInvocation(review, "current branch")
	if !strings.Contains(got, "[named phase: review]") || !strings.Contains(got, "[phase: review]") || !strings.HasSuffix(got, "current branch") {
		t.Fatalf("unexpected expansion:\n%s", got)
	}
}
