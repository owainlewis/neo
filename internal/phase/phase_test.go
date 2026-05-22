package phase

import (
	"context"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/tools"
)

func runOnce(t *testing.T, def Definition, in Input) (string, *llmtest.FakeProvider) {
	t.Helper()
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok")}}
	r := &Runner{
		Provider:     prov,
		Tools:        tools.NewRegistry(),
		DefaultModel: "test-model",
	}
	res, err := r.Run(context.Background(), def, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	return prov.Calls[0].System, prov
}

func TestRender_PlainPromptPassesThrough(t *testing.T) {
	// A prompt with no {{ }} markers must render unchanged — important
	// for backwards compatibility with prompts authored before templates.
	def := Definition{Name: "x", Prompt: "You are a helpful agent.", Source: "test"}
	sys, _ := runOnce(t, def, Input{Task: "do thing"})
	if sys != "You are a helpful agent." {
		t.Fatalf("plain prompt mangled: %q", sys)
	}
}

func TestRender_TaskSubstituted(t *testing.T) {
	def := Definition{Name: "x", Prompt: "Task is: {{.Task}}", Source: "test"}
	sys, _ := runOnce(t, def, Input{Task: "do the thing"})
	if sys != "Task is: do the thing" {
		t.Fatalf("got %q", sys)
	}
}

func TestRender_PrevAbsentTakesElseBranch(t *testing.T) {
	def := Definition{
		Name:   "x",
		Prompt: "{{if .Prev}}have prev: {{.Prev.Output}}{{else}}no prev{{end}}",
		Source: "test",
	}
	sys, _ := runOnce(t, def, Input{Task: "t"})
	if !strings.Contains(sys, "no prev") {
		t.Fatalf("expected else branch, got %q", sys)
	}
}

func TestRender_PrevPresentTakesIfBranch(t *testing.T) {
	def := Definition{
		Name:   "x",
		Prompt: "{{if .Prev}}have prev from {{.Prev.Name}}: {{.Prev.Output}}{{else}}no prev{{end}}",
		Source: "test",
	}
	prev := &StepRef{Name: "review", Output: "MARKER_TOKEN", Round: 1}
	sys, _ := runOnce(t, def, Input{Task: "t", Prev: prev, Round: 2})
	if !strings.Contains(sys, "have prev from review") {
		t.Fatalf("expected if branch, got %q", sys)
	}
	if !strings.Contains(sys, "MARKER_TOKEN") {
		t.Fatalf(".Prev.Output not substituted: %q", sys)
	}
}

func TestRender_StepsCrossReference(t *testing.T) {
	def := Definition{
		Name:   "x",
		Prompt: "plan was: {{.Steps.plan.Output}}",
		Source: "test",
	}
	steps := map[string]*StepRef{
		"plan": {Name: "plan", Output: "PLAN_TOKEN", Round: 1},
	}
	sys, _ := runOnce(t, def, Input{Task: "t", Steps: steps})
	if !strings.Contains(sys, "plan was: PLAN_TOKEN") {
		t.Fatalf("Steps lookup failed, got %q", sys)
	}
}

func TestRender_RoundExposed(t *testing.T) {
	def := Definition{Name: "x", Prompt: "round={{.Round}}", Source: "test"}
	sys, _ := runOnce(t, def, Input{Task: "t", Round: 3})
	if sys != "round=3" {
		t.Fatalf("got %q", sys)
	}
}

func TestRender_TemplateParseErrorSurfacesSource(t *testing.T) {
	def := Definition{
		Name:   "broken",
		Prompt: "{{if .Prev}}unterminated",
		Source: "test:broken.md",
	}
	r := &Runner{
		Provider:     &llmtest.FakeProvider{},
		Tools:        tools.NewRegistry(),
		DefaultModel: "m",
	}
	_, err := r.Run(context.Background(), def, Input{Task: "t"})
	if err == nil {
		t.Fatal("expected template parse error")
	}
	if !strings.Contains(err.Error(), "broken") || !strings.Contains(err.Error(), "test:broken.md") {
		t.Fatalf("error should mention step name and source path, got %v", err)
	}
}

func TestRender_UserMessageNoLongerHasArtifactsBlock(t *testing.T) {
	// Regression: the engine used to inject "# Artifacts from prior phases"
	// into the user message. With templates the step body owns prior-context
	// presentation; the user message must stay minimal.
	def := Definition{Name: "x", Prompt: "you are x", Source: "test"}
	prev := &StepRef{Name: "review", Output: "should-not-leak", Round: 1}
	_, prov := runOnce(t, def, Input{Task: "do it", Prev: prev})

	for _, m := range prov.Calls[0].Messages {
		if m.Role != llm.RoleUser {
			continue
		}
		for _, c := range m.Content {
			if c.Type == "text" {
				if strings.Contains(c.Text, "Artifacts from prior phases") {
					t.Errorf("user message still contains 'Artifacts from prior phases': %q", c.Text)
				}
				if strings.Contains(c.Text, "should-not-leak") {
					t.Errorf("prev output leaked into user message: %q", c.Text)
				}
			}
		}
	}
}
