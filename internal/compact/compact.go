package compact

import (
	"context"

	"github.com/owainlewis/neo/internal/llm"
)

type Compactor interface {
	Compact(ctx context.Context, messages []llm.Message) ([]llm.Message, error)
}

type NoCompaction struct{}

func (NoCompaction) Compact(_ context.Context, messages []llm.Message) ([]llm.Message, error) {
	return messages, nil
}

// SafeSplitPoint walks backward from desired until it finds a fresh user turn.
// Splitting there avoids keeping a tool_result without its preceding tool_use.
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
	}
	return 0
}

func hasToolResult(msg llm.Message) bool {
	for _, block := range msg.Content {
		if block.Type == "tool_result" {
			return true
		}
	}
	return false
}
