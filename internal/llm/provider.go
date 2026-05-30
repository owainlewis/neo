package llm

import "context"

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// SystemBlock is one segment of a structured system prompt. Splitting the prompt
// into blocks lets a provider cache the stable prefix (Cache == true) while the
// dynamic tail (git status, project context) varies per session without evicting
// the cached entry. Providers without caching simply concatenate the text.
type SystemBlock struct {
	Text  string
	Cache bool // mark this block as a cache breakpoint (cache it and everything before it)
}

type Request struct {
	Model    string
	System   string
	Messages []Message
	Tools    []ToolSpec
	// SystemBlocks, when non-empty, supersedes System: it carries the system
	// prompt as ordered segments so a provider can place cache breakpoints. The
	// flattened text of SystemBlocks should match System.
	SystemBlocks []SystemBlock
	MaxTokens    int
}

// Usage reports token accounting for a single completion. Cache fields are
// zero for providers that don't support prompt caching.
type Usage struct {
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int // tokens written to the cache on this request
	CacheReadTokens     int // tokens served from the cache on this request
}

type Response struct {
	Content    []ContentBlock
	StopReason string
	Usage      Usage
}

type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (*Response, error)
}
