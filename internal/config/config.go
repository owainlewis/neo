// Package config loads neo's single configuration file
// (neo.yaml / ~/.neo/config.yaml / embedded default).
package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/owainlewis/neo/internal/phase"
)

// Resolution paths and embedded fallback.
const (
	projectConfigName = "neo.yaml"
	userConfigDir     = ".neo"
	userConfigName    = "config.yaml"

	defaultModel           = "claude-opus-5"
	defaultOpenAIModel     = "gpt-5.6-sol"
	defaultCodexModel      = "gpt-5-codex"
	defaultOpenRouterModel = "anthropic/claude-sonnet-5"
	defaultGoogleModel     = "gemini-3.5-flash"
	defaultProvider        = "anthropic"

	// OpenAI auth modes (the openai_auth config key).
	OpenAIAuthAPIKey       = "api_key"
	OpenAIAuthSubscription = "subscription"
)

//go:embed defaults/neo.yaml
var embeddedConfigYAML []byte

// Config is the parsed neo.yaml.
type Config struct {
	// Provider selects the LLM backend: "anthropic" (default), "openai",
	// "openrouter", "google", or "custom" (any OpenAI-compatible endpoint).
	Provider string `yaml:"provider"`
	// OpenAIAuth selects how the "openai" provider authenticates: "api_key"
	// (default, uses OPENAI_API_KEY) or "subscription" (ChatGPT/Codex
	// device-code credentials via `neo login`). This also applies when OpenAI
	// is selected through /model while another provider is the startup default.
	OpenAIAuth string  `yaml:"openai_auth"`
	Model      string  `yaml:"model"`
	Subagents  Backend `yaml:"subagents"`
	// Custom points the "custom" provider at an arbitrary OpenAI-compatible
	// Chat Completions endpoint. BaseURL is required; the API key is read
	// from the env var named by APIKeyEnv.
	Custom     CustomBackend `yaml:"custom"`
	Features   Features      `yaml:"features"`
	Compaction Compaction    `yaml:"compaction"`
	// ToolApprovals lists optional interactive tool names and Bash prefixes.
	ToolApprovals []string `yaml:"tool_approvals"`
	Output        Output   `yaml:"output"`
	// Phases overrides Neo's built-in named prompts by name or adds custom
	// prompts. Resolution always retains defaults that are not overridden.
	Phases map[string]phase.Definition `yaml:"phases"`

	// source records where this config was loaded from (a file path or
	// "embedded"); surfaced in diagnostics via Source().
	source string
}

// CustomBackend configures the "custom" provider: any endpoint speaking the
// OpenAI-compatible Chat Completions API. The key is read from the env var
// named by APIKeyEnv (never stored in the config file).
type CustomBackend struct {
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
}

// CustomAPIKeyEnvName returns the env var the custom provider reads, falling
// back to the default when custom.api_key_env is unset. Callers that build
// configs programmatically skip load-time normalization, so the fallback
// lives here rather than only in parseConfig.
func (c *Config) CustomAPIKeyEnvName() string {
	if c.Custom.APIKeyEnv == "" {
		return DefaultCustomAPIKeyEnv
	}
	return c.Custom.APIKeyEnv
}

// DefaultCustomAPIKeyEnv names the env var the custom provider reads when
// custom.api_key_env is not set.
const DefaultCustomAPIKeyEnv = "CUSTOM_API_KEY"

// Backend optionally pins chat-spawned subagents to a provider and model.
// A zero value means subagents follow the coordinator's active backend.
type Backend struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// SubagentsConfigured reports whether subagents use a backend independent of
// the coordinator. parseConfig fills either missing half when one is supplied.
func (c *Config) SubagentsConfigured() bool {
	return c.Subagents.Provider != "" || c.Subagents.Model != ""
}

// Output configures how Neo renders tool activity during a chat session.
type Output struct {
	// Verbose restores the full tool call/result rendering (complete file
	// contents, command output, etc). Defaults to false: Neo shows live activity
	// and concise completed receipts while still surfacing errors in full.
	Verbose *bool `yaml:"verbose"`
}

// VerboseEnabled reports whether full tool output rendering is on (default: false).
func (c *Config) VerboseEnabled() bool { return featureEnabled(c.Output.Verbose, false) }

// Compaction configures when long transcripts are summarized.
type Compaction struct {
	// ContextWindowTokens is an optional manual override for the active model's
	// context window. When omitted, chat startup uses the compact package's
	// conservative default.
	ContextWindowTokens int `yaml:"context_window_tokens"`
}

// Features toggles optional, layered capabilities. The core agent loop is never
// gated by a feature — only capabilities built on top of it. Each flag is a
// tri-state *bool: nil ("absent from config") falls back to a built-in default,
// so a minimal neo.yaml still gets the full experience; set a flag to false to
// turn the capability off explicitly.
type Features struct {
	AgentsFile    *bool `yaml:"agents_file"`    // load AGENTS.md into the chat system prompt
	Skills        *bool `yaml:"skills"`         // discover and expand $name and /name skills
	PromptCaching *bool `yaml:"prompt_caching"` // cache the static system prompt prefix
}

// AgentsFileEnabled reports whether AGENTS.md loading is on (default: true).
func (c *Config) AgentsFileEnabled() bool { return featureEnabled(c.Features.AgentsFile, true) }

// SkillsEnabled reports whether skill loading is on (default: true).
func (c *Config) SkillsEnabled() bool { return featureEnabled(c.Features.Skills, true) }

// PromptCachingEnabled reports whether the static system prompt is marked for
// provider-side prompt caching (default: true).
func (c *Config) PromptCachingEnabled() bool {
	return featureEnabled(c.Features.PromptCaching, true)
}

// featureEnabled resolves a tri-state flag: nil means "unset — use the
// default"; a non-nil pointer means the user set it explicitly.
func featureEnabled(flag *bool, def bool) bool {
	if flag == nil {
		return def
	}
	return *flag
}

// Load reads the first available config file:
//
//	./neo.yaml → ~/.neo/config.yaml → embedded default
//
// First hit wins — no merging.
func Load() (*Config, error) {
	home, _ := os.UserHomeDir()

	for _, path := range configPaths(home) {
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		cfg, err := parseConfig(b, path)
		if err != nil {
			return nil, err
		}
		cfg.source = path
		return cfg, nil
	}

	cfg, err := parseConfig(embeddedConfigYAML, "embedded:neo.yaml")
	if err != nil {
		return nil, err
	}
	cfg.source = "embedded"
	return cfg, nil
}

func configPaths(home string) []string {
	paths := []string{projectConfigName}
	if home != "" {
		paths = append(paths, filepath.Join(home, userConfigDir, userConfigName))
	}
	return paths
}

func parseConfig(b []byte, source string) (*Config, error) {
	var keys map[string]yaml.Node
	if err := yaml.Unmarshal(b, &keys); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if _, ok := keys["permissions"]; ok {
		return nil, fmt.Errorf("%s: permissions has been removed; use tool_approvals for optional interactive confirmations", source)
	}

	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if c.Provider == "" {
		c.Provider = defaultProvider
	}
	if c.Provider == "openai" && c.OpenAIAuth == "" {
		c.OpenAIAuth = OpenAIAuthAPIKey
	}
	if c.Custom.APIKeyEnv == "" {
		c.Custom.APIKeyEnv = DefaultCustomAPIKeyEnv
	}
	if err := validateOpenAIAuth(c.OpenAIAuth, source); err != nil {
		return nil, err
	}
	// custom is validated before the model defaulting below: an arbitrary
	// endpoint has no sensible default model, so an omitted one is an error.
	if c.Provider == "custom" {
		if strings.TrimSpace(c.Custom.BaseURL) == "" {
			return nil, fmt.Errorf("%s: custom provider requires custom.base_url", source)
		}
		if strings.TrimSpace(c.Model) == "" {
			return nil, fmt.Errorf("%s: custom provider requires model", source)
		}
	}
	if c.Model == "" {
		c.Model = defaultModelFor(c.Provider, c.OpenAIAuth)
	}
	if c.SubagentsConfigured() {
		if c.Subagents.Provider == "" {
			c.Subagents.Provider = c.Provider
		}
		if !knownProvider(c.Subagents.Provider) {
			return nil, fmt.Errorf("%s: subagents.provider must be one of %q, %q, %q, %q, %q (got %q)",
				source, "anthropic", "openai", "openrouter", "google", "custom", c.Subagents.Provider)
		}
		if c.Subagents.Model == "" {
			c.Subagents.Model = defaultModelFor(c.Subagents.Provider, c.OpenAIAuth)
		}
	}
	seenApprovals := make(map[string]struct{}, len(c.ToolApprovals))
	approvals := make([]string, 0, len(c.ToolApprovals))
	for i, entry := range c.ToolApprovals {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("%s: tool_approvals[%d] must not be empty", source, i)
		}
		if _, duplicate := seenApprovals[entry]; duplicate {
			continue
		}
		seenApprovals[entry] = struct{}{}
		approvals = append(approvals, entry)
	}
	c.ToolApprovals = approvals
	if _, err := phase.Resolve(c.Phases); err != nil {
		return nil, fmt.Errorf("%s: phases: %w", source, err)
	}
	return &c, nil
}

// NamedPhases returns Neo's defaults with configured definitions overlaid.
// Config loading validates this data, so the error is unreachable for loaded
// configurations. Programmatically constructed zero values still get defaults.
func (c *Config) NamedPhases() []phase.Definition {
	definitions, _ := phase.Resolve(c.Phases)
	return definitions
}

func knownProvider(provider string) bool {
	switch provider {
	case "anthropic", "openai", "openrouter", "google", "custom":
		return true
	default:
		return false
	}
}

func validateOpenAIAuth(mode, source string) error {
	switch mode {
	case "", OpenAIAuthAPIKey, OpenAIAuthSubscription:
		return nil
	default:
		return fmt.Errorf("%s: openai_auth must be one of %q, %q (got %q)", source, OpenAIAuthAPIKey, OpenAIAuthSubscription, mode)
	}
}

// SubscriptionAuth reports whether the openai provider should authenticate via
// a ChatGPT/Codex subscription rather than an API key.
func (c *Config) SubscriptionAuth() bool {
	return c.Provider == "openai" && c.OpenAIAuth == OpenAIAuthSubscription
}

// defaultModelFor returns the default model when the config omits an explicit
// one, accounting for the openai subscription backend's distinct model ids.
func defaultModelFor(provider, openAIAuth string) string {
	switch provider {
	case "openai":
		if openAIAuth == OpenAIAuthSubscription {
			return defaultCodexModel
		}
		return defaultOpenAIModel
	case "openrouter":
		return defaultOpenRouterModel
	case "google":
		return defaultGoogleModel
	default:
		return defaultModel
	}
}

// Source describes where this Config was loaded from (a file path or
// "embedded"). Useful in error messages and diagnostics.
func (c *Config) Source() string { return c.source }
