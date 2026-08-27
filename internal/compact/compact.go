package compact

import (
	"context"

	"github.com/owainlewis/neo/internal/llm"
)

type Compactor interface {
	// Compact trims messages when the transcript no longer fits. promptTokens is
	// what the provider reported for the previous request, or 0 before any
	// response has landed.
	Compact(ctx context.Context, messages []llm.Message, promptTokens int) (Result, error)
}

// Result contains the transcript and provider usage produced by a compaction
// attempt. Usage can be non-zero even when compaction returns an error, because
// a provider response may still be billable even if its summary is unusable.
type Result struct {
	Messages []llm.Message
	Usage    llm.Usage
}

type NoCompaction struct{}

func (NoCompaction) Compact(_ context.Context, messages []llm.Message, _ int) (Result, error) {
	return Result{Messages: messages}, nil
}

// SafeSplitPoint walks backward from desired until it finds either a fresh user
// turn or the end of a completed tool-use exchange. Splitting at either boundary
// keeps every retained tool_result paired with its preceding tool_use.
func SafeSplitPoint(messages []llm.Message, desired int) int {
	if desired <= 0 {
		return 0
	}
	if desired >= len(messages) {
		return len(messages)
	}
	for i := desired; i > 0; i-- {
		if messages[i].Role == llm.RoleUser && !hasToolResult(messages[i]) {
			return i
		}
		if completedToolExchangeBefore(messages, i) {
			return i
		}
	}
	return 0
}

func completedToolExchangeBefore(messages []llm.Message, split int) bool {
	if split < 2 {
		return false
	}
	uses := messages[split-2]
	results := messages[split-1]
	if uses.Role != llm.RoleAssistant || results.Role != llm.RoleUser {
		return false
	}
	pending := make(map[string]int)
	for _, block := range uses.Content {
		if block.Type == "tool_use" {
			pending[block.ID]++
		}
	}
	if len(pending) == 0 {
		return false
	}
	matched := 0
	for _, block := range results.Content {
		if block.Type != "tool_result" {
			continue
		}
		if pending[block.ToolUseID] == 0 {
			return false
		}
		pending[block.ToolUseID]--
		matched++
	}
	if matched == 0 {
		return false
	}
	for _, count := range pending {
		if count != 0 {
			return false
		}
	}
	return true
}

func hasToolResult(msg llm.Message) bool {
	for _, block := range msg.Content {
		if block.Type == "tool_result" {
			return true
		}
	}
	return false
}
