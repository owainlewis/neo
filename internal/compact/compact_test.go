package compact

import (
	"context"
	"reflect"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
)

func TestNoCompactionPreservesMessages(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}}
	got, err := NoCompaction{}.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if !reflect.DeepEqual(got, msgs) {
		t.Fatalf("NoCompaction changed messages: %#v", got)
	}
}

func TestSafeSplitPointAvoidsToolResults(t *testing.T) {
	msgs := []llm.Message{
		userText("first"),
		assistantTool("t1"),
		userToolResult("t1"),
		userText("second"),
		assistantText("done"),
	}
	if got := SafeSplitPoint(msgs, 2); got != 0 {
		t.Fatalf("split at 2 = %d, want 0", got)
	}
	if got := SafeSplitPoint(msgs, 4); got != 3 {
		t.Fatalf("split at 4 = %d, want 3", got)
	}
}

func userText(s string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: s}}}
}

func assistantText(s string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "text", Text: s}}}
}

func assistantTool(id string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "tool_use", ID: id}}}
}

func userToolResult(id string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "tool_result", ToolUseID: id}}}
}
