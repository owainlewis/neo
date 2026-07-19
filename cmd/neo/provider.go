package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/auth"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/anthropic"
	"github.com/owainlewis/neo/internal/llm/google"
	"github.com/owainlewis/neo/internal/llm/openai"
	"github.com/owainlewis/neo/internal/llm/openrouter"
	"github.com/owainlewis/neo/internal/session"
	"github.com/owainlewis/neo/internal/tui"
)

func mustProvider(cfg *config.Config) llm.Provider {
	prov, err := newProvider(cfg, cfg.Provider)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return prov
}

func newProvider(cfg *config.Config, name string) (llm.Provider, error) {
	switch name {
	case "openai":
		if cfg.OpenAIAuth == config.OpenAIAuthSubscription {
			return newCodexProvider()
		}
		return openai.New()
	case "openrouter":
		return openrouter.New()
	case "google":
		return google.New()
	case "anthropic", "":
		return anthropic.New()
	default:
		return nil, fmt.Errorf("unknown provider %q (expected \"anthropic\", \"openai\", \"openrouter\", or \"google\")", name)
	}
}

func checkedProvider(ctx context.Context, cfg *config.Config, name string) (llm.Provider, error) {
	if name == "openai" && cfg.OpenAIAuth == config.OpenAIAuthSubscription {
		store, err := auth.DefaultStore()
		if err != nil {
			return nil, err
		}
		if _, err := auth.NewTokenSource(store, auth.ProviderOpenAICodex).Token(ctx); err != nil {
			return nil, fmt.Errorf("OpenAI subscription credentials: %w", err)
		}
	}
	return newProvider(cfg, name)
}

func chatSessionProvider(ctx context.Context, cfg *config.Config, sess *session.Session, name string) (llm.Provider, error) {
	if sess != nil {
		return checkedProvider(ctx, cfg, name)
	}
	return newProvider(cfg, name)
}

// newCodexProvider builds the ChatGPT/Codex subscription client from stored
// device-code credentials, erroring clearly if the user hasn't logged in.
func newCodexProvider() (llm.Provider, error) {
	store, err := auth.DefaultStore()
	if err != nil {
		return nil, err
	}
	if _, ok, err := store.Get(auth.ProviderOpenAICodex); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("not logged in to an OpenAI subscription: run `neo login`")
	}
	src := auth.NewTokenSource(store, auth.ProviderOpenAICodex)
	return openai.NewCodex(codexCredentials{ts: src}), nil
}

// codexCredentials adapts auth.TokenSource to openai.CredentialSource.
type codexCredentials struct{ ts *auth.TokenSource }

func (c codexCredentials) Token(ctx context.Context) (accessToken, accountID string, err error) {
	cr, err := c.ts.Token(ctx)
	if err != nil {
		return "", "", err
	}
	return cr.AccessToken, cr.AccountID, nil
}

// runLogin performs the OpenAI subscription device-code flow and stores the
// result.
func runLogin(ctx context.Context) {
	store, err := auth.DefaultStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "login: %v\n", err)
		os.Exit(1)
	}

	creds, err := auth.LoginOpenAI(ctx, auth.LoginOptions{
		OnDeviceCode: func(url, code string) {
			fmt.Println("Log in to OpenAI with this device code:")
			fmt.Println("\n  " + url)
			fmt.Println("  Code: " + code + "\n")
			fmt.Println("The code expires after 15 minutes. Never share it.")
			fmt.Println("Waiting for authorization to complete...")
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "login failed: %v\n", err)
		os.Exit(1)
	}
	if err := store.Set(auth.ProviderOpenAICodex, creds); err != nil {
		fmt.Fprintf(os.Stderr, "save credentials: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Login complete. Credentials saved to " + store.Path() + ".")
	fmt.Println("Set `provider: openai` and `openai_auth: subscription` in neo.yaml to use them.")
}

// runLogout removes stored subscription credentials.
func runLogout() {
	store, err := auth.DefaultStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "logout: %v\n", err)
		os.Exit(1)
	}
	if err := store.Delete(auth.ProviderOpenAICodex); err != nil {
		fmt.Fprintf(os.Stderr, "logout: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Logged out of OpenAI subscription.")
}

func sessionBackend(cfg *config.Config, meta session.Metadata) (string, string) {
	if meta.Provider == "" || meta.Model == "" {
		return cfg.Provider, cfg.Model
	}
	if meta.Provider == cfg.Provider || providerCredentialPresent(cfg, meta.Provider) {
		return meta.Provider, meta.Model
	}
	fmt.Fprintf(os.Stderr, "warning: session provider %s is not configured; continuing with %s model %s\n",
		meta.Provider, cfg.Provider, cfg.Model)
	return cfg.Provider, cfg.Model
}

func modelChoices(ctx context.Context, cfg *config.Config, activeProvider string) []tui.ModelChoice {
	var choices []tui.ModelChoice
	for _, provider := range configuredProviders(cfg, activeProvider) {
		providerChoices := providerModelChoices(ctx, cfg, provider, provider == activeProvider)
		for i := range providerChoices {
			providerChoices[i].Provider = provider
		}
		choices = append(choices, providerChoices...)
	}
	return choices
}

// configuredProviders treats an available local credential source as provider
// configuration. The active provider is always first because chat startup has
// already validated it; additional providers require their credential to be
// present before they enter the picker.
func configuredProviders(cfg *config.Config, active string) []string {
	if active == "" {
		active = cfg.Provider
		if active == "" {
			active = "anthropic"
		}
	}
	ordered := []string{active, "anthropic", "openai", "openrouter", "google"}
	seen := map[string]bool{}
	var providers []string
	for _, provider := range ordered {
		if seen[provider] || (provider != active && !providerCredentialPresent(cfg, provider)) {
			continue
		}
		seen[provider] = true
		providers = append(providers, provider)
	}
	return providers
}

func providerCredentialPresent(cfg *config.Config, provider string) bool {
	switch provider {
	case "anthropic":
		return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != ""
	case "openai":
		if cfg.OpenAIAuth == config.OpenAIAuthSubscription {
			store, err := auth.DefaultStore()
			if err != nil {
				return false
			}
			_, ok, err := store.Get(auth.ProviderOpenAICodex)
			return err == nil && ok
		}
		return strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != ""
	case "openrouter":
		return strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) != ""
	case "google":
		return strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) != ""
	default:
		return false
	}
}

func providerModelChoices(ctx context.Context, cfg *config.Config, provider string, active bool) []tui.ModelChoice {
	switch provider {
	case "openai":
		if cfg.OpenAIAuth == config.OpenAIAuthSubscription {
			return []tui.ModelChoice{
				{ID: "gpt-5-codex", Name: "GPT-5 Codex", Description: "Supported ChatGPT/Codex subscription model"},
			}
		}
		return []tui.ModelChoice{
			{ID: "gpt-5.2", Name: "GPT-5.2", Description: "Recommended flagship model for coding and agentic tasks"},
			{ID: "gpt-5.1", Name: "GPT-5.1", Description: "Coding and agentic model with configurable reasoning"},
			{ID: "gpt-5", Name: "GPT-5", Description: "Previous GPT-5 reasoning model"},
			{ID: "gpt-5-mini", Name: "GPT-5 mini", Description: "Faster, lower-cost GPT-5 model"},
			{ID: "gpt-5-nano", Name: "GPT-5 nano", Description: "Smallest GPT-5 model"},
			{ID: "gpt-4.1", Name: "GPT-4.1", Description: "Non-reasoning model for general coding tasks"},
			{ID: "gpt-4o", Name: "GPT-4o", Description: "Fast multimodal GPT-4o model"},
			{ID: "gpt-4o-mini", Name: "GPT-4o mini", Description: "Smaller GPT-4o model"},
		}
	case "openrouter":
		if !active {
			return []tui.ModelChoice{
				{ID: openrouter.DefaultModel, Name: openrouter.DefaultModel, Description: "Default OpenRouter model"},
			}
		}
		return openRouterModelChoices(ctx)
	case "google":
		return []tui.ModelChoice{
			{ID: google.DefaultModel, Name: "Gemini 3.5 Flash", Description: "Stable Google Gemini model for coding and agentic tasks"},
			{ID: "gemini-3.1-pro-preview", Name: "Gemini 3.1 Pro Preview", Description: "Higher-capability preview model for complex coding tasks"},
			{ID: "gemini-3.1-flash-lite", Name: "Gemini 3.1 Flash-Lite", Description: "Lower-cost stable Gemini model"},
		}
	default:
		return []tui.ModelChoice{
			{ID: "claude-opus-4-8", Name: "Claude Opus 4.8", Description: "Default Anthropic model"},
		}
	}
}

// openRouterModelChoices fetches the live OpenRouter model catalogue. Model ids
// move fast, so the picker is populated from OpenRouter's /models endpoint rather
// than a hardcoded list. On failure (offline, timeout, API change) it falls back
// to the provider default so the picker still works. The fetch is time-boxed so
// startup never hangs on a slow network.
func openRouterModelChoices(ctx context.Context) []tui.ModelChoice {
	fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	models, err := openrouter.Models(fetchCtx, nil)
	if err != nil || len(models) == 0 {
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not fetch OpenRouter models (%v); using default\n", err)
		}
		return []tui.ModelChoice{
			{ID: openrouter.DefaultModel, Name: openrouter.DefaultModel, Description: "Default OpenRouter model"},
		}
	}

	choices := make([]tui.ModelChoice, 0, len(models))
	for _, m := range models {
		name := m.Name
		if name == "" {
			name = m.ID
		}
		choices = append(choices, tui.ModelChoice{ID: m.ID, Name: name, Description: m.Description})
	}
	return choices
}
