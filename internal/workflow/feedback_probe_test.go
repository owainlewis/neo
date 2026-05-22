package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
)

// Empirical: when review fails and the engine retries from write, what does
// the round-2 write step actually receive in its user message? This is the
// concrete answer to "does the builder see the reviewer's feedback?".
//
// Today the answer is "yes, but only as a bullet of the generic 'Artifacts
// from prior phases' block — no special section, no signal that it's
// actionable feedback".  See GH #49.
func TestProbe_WriteSeesReviewFeedbackOnRetry(t *testing.T) {
	eng, _, prov := testHarness(t,
		[]string{"write", "review"},
		[]llm.Response{
			llmtest.Text("first attempt — buggy"),
			llmtest.Text("```neo-result\n{\"status\":\"fail\",\"summary\":\"REVIEW_SAW_NIL\"}\n```"),
			llmtest.Text("second attempt — fixed"),
			llmtest.Text("```neo-result\n{\"status\":\"pass\"}\n```"),
		},
	)

	if err := eng.Run(context.Background(), Definition{
		Name: "demo", Steps: []string{"write", "review"},
		RetryFrom: "write", MaxRounds: 2,
	}, "the original task"); err != nil {
		t.Fatalf("engine.Run: %v", err)
	}

	// 4 calls: write/r1, review/r1, write/r2, review/r2.
	if len(prov.Calls) != 4 {
		t.Fatalf("expected 4 calls, got %d", len(prov.Calls))
	}

	r2Write := prov.Calls[2]
	t.Logf("=== round-2 write system prompt ===\n%s\n=== end system ===", r2Write.System)
	user := flattenUserText(r2Write.Messages)
	t.Logf("=== round-2 write user message ===\n%s\n=== end user ===", user)

	// The actual assertion: does the verdict text appear in the user msg?
	if !strings.Contains(user, "REVIEW_SAW_NIL") {
		t.Errorf("round-2 write did NOT see review feedback (REVIEW_SAW_NIL token missing)")
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
