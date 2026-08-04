package tools

import "context"

type executionContextKey struct{}
type conversationGenerationKey struct{}

// CallMetadata identifies one model-requested tool execution. It is carried
// through context so tools that supervise nested work can attribute their
// events without exposing scheduler fields in model-facing schemas.
type CallMetadata struct {
	ToolUseID string
	GroupID   string
	GroupSize int
	GroupPos  int
}

func WithCallMetadata(ctx context.Context, metadata CallMetadata) context.Context {
	return context.WithValue(ctx, executionContextKey{}, metadata)
}

func CallMetadataFrom(ctx context.Context) (CallMetadata, bool) {
	metadata, ok := ctx.Value(executionContextKey{}).(CallMetadata)
	return metadata, ok
}

// WithConversationGeneration tags work started by one TUI conversation. Tools
// copy the value onto buffered UI events at production time, before those
// events can be delayed by an intermediate channel.
func WithConversationGeneration(ctx context.Context, generation uint64) context.Context {
	return context.WithValue(ctx, conversationGenerationKey{}, generation)
}

// ConversationGenerationFrom returns the TUI conversation generation carried
// by ctx. Zero is the initial generation and the default outside the TUI.
func ConversationGenerationFrom(ctx context.Context) uint64 {
	generation, _ := ctx.Value(conversationGenerationKey{}).(uint64)
	return generation
}
