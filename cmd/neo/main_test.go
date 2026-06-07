package main

import (
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
)

func TestModelChoices_OpenAISubscriptionOnlyListsSupportedCodexModel(t *testing.T) {
	choices := modelChoices(&config.Config{
		Provider:   "openai",
		OpenAIAuth: config.OpenAIAuthSubscription,
	})

	if len(choices) != 1 {
		t.Fatalf("subscription choices = %d, want 1: %#v", len(choices), choices)
	}
	if choices[0].ID != "gpt-5-codex" {
		t.Fatalf("subscription model = %q, want gpt-5-codex", choices[0].ID)
	}
}

func TestModelChoices_OpenAIAPIKeyDoesNotListCodexModels(t *testing.T) {
	choices := modelChoices(&config.Config{
		Provider:   "openai",
		OpenAIAuth: config.OpenAIAuthAPIKey,
	})

	for _, choice := range choices {
		if strings.Contains(choice.ID, "codex") {
			t.Fatalf("api-key model picker should not list Codex model %q", choice.ID)
		}
	}
}
