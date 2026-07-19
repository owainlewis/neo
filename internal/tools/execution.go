package tools

import "context"

type executionContextKey struct{}

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
