// Package custom implements the llm.Provider interface against any
// OpenAI-compatible Chat Completions endpoint described by custom.base_url.
package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/llm/chatcompletions"
)

const (
	ProviderName = "custom"

	// DefaultAPIKeyEnv names the env var the custom provider reads when
	// custom.api_key_env is not set.
	DefaultAPIKeyEnv = "CUSTOM_API_KEY"
)

// New constructs a custom-endpoint provider. The API key is read from the env
// var named by apiKeyEnv (custom.api_key_env, defaulting to CUSTOM_API_KEY).
func New(baseURL, apiKeyEnv, defaultModel string) (*chatcompletions.Client, error) {
	if apiKeyEnv == "" {
		apiKeyEnv = DefaultAPIKeyEnv
	}
	key := os.Getenv(apiKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("%s is not set", apiKeyEnv)
	}
	return &chatcompletions.Client{
		ProviderName: ProviderName,
		APIKey:       key,
		Endpoint:     strings.TrimSuffix(baseURL, "/") + "/chat/completions",
		DefaultModel: defaultModel,
		HTTP:         &http.Client{Timeout: 5 * time.Minute},
		MaxRetries:   4,
		BaseDelay:    500 * time.Millisecond,
	}, nil
}

// Model describes a single entry from a custom endpoint's /models listing.
type Model struct {
	ID          string
	Name        string
	Description string
}

// Models fetches the model catalogue from GET baseURL/models (the standard
// OpenAI-compatible listing). Unlike OpenRouter's, this endpoint usually
// requires auth, so the request carries the key from the env var named by
// apiKeyEnv when one is set. Results are sorted by id for a stable picker
// order. Endpoints are free to deviate, so callers should treat errors as a
// signal to fall back to the configured model.
//
// httpClient may be nil, in which case a client with a short timeout is used.
func Models(ctx context.Context, httpClient *http.Client, baseURL, apiKeyEnv string) ([]Model, error) {
	if apiKeyEnv == "" {
		apiKeyEnv = DefaultAPIKeyEnv
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("custom models: %w", err)
	}
	if key := os.Getenv(apiKeyEnv); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("custom models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("custom models: unexpected status %s", resp.Status)
	}

	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("custom models: decode: %w", err)
	}

	models := make([]Model, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID == "" {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.ID
		}
		models = append(models, Model{ID: m.ID, Name: name, Description: m.Description})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}
