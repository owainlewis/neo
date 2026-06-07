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
		"`internal/auth/` | OpenAI ChatGPT/Codex device-code login",
		"`openai_auth: subscription` builds the Codex subscription provider from stored device-code credentials",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("architecture page missing %q", want)
		}
	}
}

func TestCLIPageDocumentsOpenAIAuthCommands(t *testing.T) {
	page := cliPage()

	for _, want := range []string{
		"`neo login` | Log in to an OpenAI ChatGPT/Codex subscription with device-code auth.",
		"`neo logout` | Remove stored OpenAI subscription credentials.",
		"`OPENAI_API_KEY` is required when `provider: openai` uses `openai_auth: api_key`.",
		"`openai_auth: subscription` uses stored ChatGPT/Codex device-code credentials",
		"prints the OpenAI Codex device-code URL and one-time code",
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
		"ChatGPT/Codex device-code credentials from `~/.neo/auth.json`",
		"token values are never generated into developer docs",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("config page missing %q", want)
		}
	}
}

func TestSessionsPageDocumentsTUIBrowser(t *testing.T) {
	page := sessionsPage()

	for _, want := range []string{
		"`/sessions` opens an in-TUI session browser",
		"cwd/all filtering",
		"only resumes sessions from the current cwd",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("sessions page missing %q", want)
		}
	}
}

func TestIndexLinksTeachingGuides(t *testing.T) {
	page := indexPage()

	if !strings.Contains(page, "[Teaching guides](guides/index.md)") {
		t.Fatalf("index page missing teaching guides link")
	}
}

func TestTeachingGuidesCoverCoreFeatures(t *testing.T) {
	page := guidesIndexPage()

	for _, want := range []string{
		"[Agent loop](agent-loop.md)",
		"[System prompt](system-prompt.md)",
		"[Tools](tools.md)",
		"[Permissions](permissions.md)",
		"[Providers](providers.md)",
		"[Sessions](sessions.md)",
		"[Compaction](compaction.md)",
		"[Memory](memory.md)",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("guides index missing %q", want)
		}
	}
}

func TestPermissionsGuideExplainsModes(t *testing.T) {
	page := permissionsGuidePage()

	for _, want := range []string{
		"`ask`",
		"`trusted`",
		"`readonly`",
		"To turn approval prompts off",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("permissions guide missing %q", want)
		}
	}
}

func TestMemoryGuideSeparatesAgentsAndMemory(t *testing.T) {
	page := memoryGuidePage()

	for _, want := range []string{
		"AGENTS.md is instruction",
		"MEMORY.md would be learned context",
		"experimental and off by default",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("memory guide missing %q", want)
		}
	}
}
