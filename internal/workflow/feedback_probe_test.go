package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/artifact"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/phase"
	"github.com/owainlewis/neo/internal/tools"
)

// Empirical: when review fails and the engine retries from write, the
// round-2 write step must see the reviewer's verdict — now via the
// step prompt's {{.Prev.Output}} template, not the engine's user message.
//
// This locks down the contract: the verdict reaches the next step *through*
// the template, so any change that breaks the .Prev wiring is visible here.
func TestProbe_WriteSeesReviewFeedbackOnRetry(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.Text("first attempt — buggy"),
		llmtest.Text("```neo-result\n{\"status\":\"fail\",\"summary\":\"REVIEW_SAW_NIL\"}\n```"),
		llmtest.Text("second attempt — fixed"),
		llmtest.Text("```neo-result\n{\"status\":\"pass\"}\n```"),
	}}

	// `write` step uses a template that conditionally inlines the prior
	// step's output; `review` is plain text.
	writeTpl := `BUILDER.
{{if .Prev}}feedback from {{.Prev.Name}}: {{.Prev.Output}}{{else}}fresh build{{end}}`

	resolver := &fakeResolver{steps: map[string]phase.Definition{
		"write":  {Name: "write", Prompt: writeTpl, Source: "test:write"},
		"review": {Name: "review", Prompt: "REVIEWER prompt", Source: "test:review"},
	}}
	pr := &phase.Runner{Provider: prov, Tools: tools.NewRegistry(), DefaultModel: "m"}
	eng := &Engine{
		Resolver: resolver,
		Runner:   pr,
		Store:    artifact.NewStore(t.TempDir()),
		Sink:     &recordingSink{},
	}

	if err := eng.Run(context.Background(), Definition{
		Name: "demo", Steps: []string{"write", "review"},
		RetryFrom: "write", MaxRounds: 2,
	}, "the original task"); err != nil {
		t.Fatalf("engine.Run: %v", err)
	}

	if len(prov.Calls) != 4 {
		t.Fatalf("expected 4 calls, got %d", len(prov.Calls))
	}

	// Round 1 write: no .Prev → fresh-build branch fires.
	r1Write := prov.Calls[0]
	if !strings.Contains(r1Write.System, "fresh build") {
		t.Errorf("round-1 write system should take {{else}} branch, got:\n%s", r1Write.System)
	}

	// Round 2 write: .Prev is the reviewer → its output is inlined.
	r2Write := prov.Calls[2]
	t.Logf("=== round-2 write system prompt ===\n%s\n=== end ===", r2Write.System)
	if !strings.Contains(r2Write.System, "REVIEW_SAW_NIL") {
		t.Errorf("round-2 write system did NOT see review feedback via .Prev.Output")
	}
	if !strings.Contains(r2Write.System, "feedback from review") {
		t.Errorf("round-2 write system did NOT take {{if .Prev}} branch")
	}

	// The user message is now minimal — no artifacts dump.
	for _, call := range prov.Calls {
		user := flattenUserText(call.Messages)
		if strings.Contains(user, "Artifacts from prior phases") {
			t.Errorf("engine should no longer inject Artifacts block into user message; got:\n%s", user)
		}
	}
}

func flattenUserText(msgs []llm.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		if m.Role != llm.RoleUser {
			continue
		}
		for _, c := range m.Content {
			if c.Type == "text" {
				sb.WriteString(c.Text)
				sb.WriteString("\n")
			}
		}
	}
	return sb.String()
}
