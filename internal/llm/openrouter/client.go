// Package openrouter implements the llm.Provider interface against
// OpenRouter's OpenAI-compatible Chat Completions API.
package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/owainlewis/neo/internal/llm/chatcompletions"
)

const (
	DefaultEndpoint = "https://openrouter.ai/api/v1/chat/completions"
	DefaultModel    = "anthropic/claude-sonnet-4.5"
)

// ModelsEndpoint returns OpenRouter's live model catalogue. It is public
// (no API key required), so the picker can populate even before a key is set.
// It is a var so tests can point it at a local server.
var ModelsEndpoint = "https://openrouter.ai/api/v1/models"

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

// Model describes a single entry from OpenRouter's live model catalogue.
type Model struct {
	ID          string
	Name        string
	Description string
}

// Models fetches the live model catalogue from OpenRouter's public /models
// endpoint. It is the source of truth for available model ids — names move fast,
// so callers should prefer this over any hardcoded list. Results are sorted by id
// for a stable picker order.
//
// httpClient may be nil, in which case a client with a short timeout is used.
func Models(ctx context.Context, httpClient *http.Client) ([]Model, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ModelsEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("openrouter models: %w", err)
	}
	// The endpoint is public; include the key if present for higher rate limits.
	if key := os.Getenv("OPENROUTER_API_KEY"); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter models: unexpected status %s", resp.Status)
	}

	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("openrouter models: decode: %w", err)
	}

	models := make([]Model, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, Model{ID: m.ID, Name: m.Name, Description: m.Description})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}
