// Package openrouter implements the llm.Provider interface against
// OpenRouter's OpenAI-compatible Chat Completions API.
package openrouter

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/owainlewis/neo/internal/llm/chatcompletions"
)

const (
	DefaultEndpoint = "https://openrouter.ai/api/v1/chat/completions"
	DefaultModel    = "anthropic/claude-sonnet-4.5"
)

// New constructs an OpenRouter provider from OPENROUTER_API_KEY.
func New() (*chatcompletions.Client, error) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY is not set")
	}
	return &chatcompletions.Client{
		ProviderName: "openrouter",
		APIKey:       key,
		Endpoint:     DefaultEndpoint,
		DefaultModel: DefaultModel,
		HTTP:         &http.Client{Timeout: 5 * time.Minute},
		MaxRetries:   4,
		BaseDelay:    500 * time.Millisecond,
	}, nil
}
