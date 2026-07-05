package compact

import (
	"context"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
)

// turns builds n alternating user/assistant messages, each ~400 chars (~100
// estimated tokens).
func turns(n int) []llm.Message {
	filler := strings.Repeat("x", 400)
	msgs := make([]llm.Message, 0, n)
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			msgs = append(msgs, userText(filler))
		} else {
			msgs = append(msgs, assistantText(filler))
		}
	}
	return msgs
}

func TestSummarizer_BelowTriggerIsNoOp(t *testing.T) {
	prov := &llmtest.FakeProvider{}
	s := Summarizer{Provider: prov, Model: "m", TriggerTokens: 1_000_000, KeepRecent: 2}

	msgs := turns(10)
	out, err := s.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != len(msgs) {
		t.Fatalf("messages changed below trigger: got %d, want %d", len(out), len(msgs))
	}
	if len(prov.Calls) != 0 {
		t.Fatalf("provider called %d times below trigger, want 0", len(prov.Calls))
	}
}

func TestTriggerTokensForContextWindow(t *testing.T) {
	if got := TriggerTokensForContextWindow(200_000); got != 140_000 {
		t.Fatalf("trigger = %d, want 140000", got)
	}
	if got := TriggerTokensForContextWindow(0); got != 140_000 {
		t.Fatalf("default trigger = %d, want 140000", got)
	}
}

func TestSummarizer_CompactsOldTurnsIntoSummary(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("the summary")}}
	s := Summarizer{Provider: prov, Model: "m", TriggerTokens: 1, KeepRecent: 4}

	msgs := turns(12)
	out, err := s.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Split lands on the user turn at index 8: one summary message plus the
	// kept tail.
	if len(out) != 5 {
		t.Fatalf("compacted length = %d, want 5", len(out))
	}
	first := out[0]
	if first.Role != llm.RoleUser || !strings.Contains(first.Content[0].Text, "the summary") {
		t.Fatalf("first message is not the user summary: %+v", first)
	}
	for i, want := range msgs[8:] {
		if out[i+1].Content[0].Text != want.Content[0].Text {
			t.Fatalf("kept tail message %d does not match original", i)
		}
	}

	// The summarization request carries the head plus a final user instruction.
	if len(prov.Calls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.Calls))
	}
	req := prov.Calls[0]
	if len(req.Messages) != 9 {
		t.Fatalf("summary request messages = %d, want 9 (8 head + instruction)", len(req.Messages))
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != llm.RoleUser || last.Content[0].Text != summaryInstruction {
		t.Fatalf("summary request does not end with the instruction: %+v", last)
	}
	if req.System != summarySystem {
		t.Fatal("summary request does not carry the summary system prompt")
	}
}

func TestSummarizer_ProviderErrorPropagates(t *testing.T) {
	prov := &llmtest.FakeProvider{} // no scripted responses → Complete errors
	s := Summarizer{Provider: prov, Model: "m", TriggerTokens: 1, KeepRecent: 2}

	if _, err := s.Compact(context.Background(), turns(10)); err == nil {
		t.Fatal("expected summarization error to propagate")
	}
}

func TestSummarizer_NoSafeSplitLeavesTranscriptAlone(t *testing.T) {
	prov := &llmtest.FakeProvider{}
	s := Summarizer{Provider: prov, Model: "m", TriggerTokens: 1, KeepRecent: 1}

	// Every user message carries a tool_result, so there is no safe split.
	filler := strings.Repeat("x", 400)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "tool_result", ToolUseID: "t1", Content: filler}}},
		assistantText(filler),
	}
	out, err := s.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != len(msgs) || len(prov.Calls) != 0 {
		t.Fatal("transcript with no safe split point should be left unchanged")
	}
}

func TestSummarizer_EmptySummaryIsAnError(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("")}}
	s := Summarizer{Provider: prov, Model: "m", TriggerTokens: 1, KeepRecent: 2}

	if _, err := s.Compact(context.Background(), turns(10)); err == nil {
		t.Fatal("expected error when summarization returns no text")
	}
}
