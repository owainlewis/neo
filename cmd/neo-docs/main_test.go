package main

import (
	"strings"
	"testing"
)

func TestArchitecturePageDocumentsProviderAndAuthModules(t *testing.T) {
	page := architecturePage()

	for _, want := range []string{
		"`internal/llm/anthropic/` | Anthropic provider adapter.",
		"`internal/llm/openai/` | OpenAI provider adapters",
		"`internal/auth/` | OpenAI ChatGPT/Codex OAuth login",
		"`openai_auth: subscription` builds the Codex subscription provider from stored OAuth credentials",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("architecture page missing %q", want)
		}
	}
}

func TestCLIPageDocumentsOpenAIAuthCommands(t *testing.T) {
	page := cliPage()

	for _, want := range []string{
		"`neo login` | Log in to an OpenAI ChatGPT/Codex subscription with OAuth.",
		"`neo logout` | Remove stored OpenAI subscription credentials.",
		"`OPENAI_API_KEY` is required when `provider: openai` uses `openai_auth: api_key`.",
		"`openai_auth: subscription` uses stored ChatGPT/Codex OAuth credentials",
		"`~/.neo/auth.json`",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("CLI page missing %q", want)
		}
	}
}

func TestConfigPageDocumentsOpenAIAuthModesWithoutSecrets(t *testing.T) {
	page := configPage("provider: anthropic\nopenai_auth: api_key\n")

	for _, want := range []string{
		"`provider: openai` with `openai_auth: api_key`",
		"`OPENAI_API_KEY`",
		"`provider: openai` with `openai_auth: subscription`",
		"ChatGPT/Codex OAuth credentials from `~/.neo/auth.json`",
		"token values are never generated into developer docs",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("config page missing %q", want)
		}
	}
}
