package llm

import (
	"context"
	"encoding/json"
)

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
	// Raw preserves provider-specific content blocks that must be replayed in
	// later turns but are otherwise opaque to the core agent loop.
	Raw json.RawMessage `json:"raw,omitempty"`
	// Source carries image data for blocks of Type "image". It maps to
	// Anthropic's image source object (base64-encoded bytes + media type).
	Source *ImageSource `json:"source,omitempty"`
}

// ImageSource is the payload for an image content block. Today only base64
// inline data is supported (Type == "base64"); a URL variant can be added
// later without touching callers.
type ImageSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // e.g. "image/png"
	Data      string `json:"data"`       // base64-encoded bytes
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
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"` // tokens written to the cache on this request
	CacheReadTokens     int `json:"cache_read_tokens"`     // tokens served from the cache on this request
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
