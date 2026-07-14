// Package config loads neo's single configuration file
// (neo.yaml / ~/.neo/config.yaml / embedded default).
package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Resolution paths and embedded fallback.
const (
	projectConfigName = "neo.yaml"
	userConfigDir     = ".neo"
	userConfigName    = "config.yaml"

	defaultModel           = "claude-opus-4-8"
	defaultOpenAIModel     = "gpt-4o"
	defaultCodexModel      = "gpt-5-codex"
	defaultOpenRouterModel = "anthropic/claude-sonnet-4.5"
	defaultGoogleModel     = "gemini-3.5-flash"
	defaultProvider        = "anthropic"

	// OpenAI auth modes (the openai_auth config key).
	OpenAIAuthAPIKey       = "api_key"
	OpenAIAuthSubscription = "subscription"

	PermissionModeAsk      = "ask"
	PermissionModeTrusted  = "trusted"
	PermissionModeReadonly = "readonly"
)

//go:embed defaults/neo.yaml
var embeddedConfigYAML []byte

// Config is the parsed neo.yaml.
type Config struct {
	// Provider selects the LLM backend: "anthropic" (default), "openai", "openrouter", or "google".
	Provider string `yaml:"provider"`
	// OpenAIAuth selects how the "openai" provider authenticates: "api_key"
	// (default, uses OPENAI_API_KEY) or "subscription" (ChatGPT/Codex
	// device-code credentials via `neo login`). Ignored for other providers.
	OpenAIAuth  string      `yaml:"openai_auth"`
	Model       string      `yaml:"model"`
	Features    Features    `yaml:"features"`
	Compaction  Compaction  `yaml:"compaction"`
	Permissions Permissions `yaml:"permissions"`
	Output      Output      `yaml:"output"`

	// source records where this config was loaded from (a file path or
	// "embedded"); surfaced in diagnostics via Source().
	source string
}

// Permissions configures how Neo gates tool calls before they run.
type Permissions struct {
	Mode string `yaml:"mode"`
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
	Memory        *bool `yaml:"memory"`         // load and update project-root memory.md
	Skills        *bool `yaml:"skills"`         // discover and expand $name and /name skills
	PromptCaching *bool `yaml:"prompt_caching"` // cache the static system prompt prefix
}

// AgentsFileEnabled reports whether AGENTS.md loading is on (default: true).
func (c *Config) AgentsFileEnabled() bool { return featureEnabled(c.Features.AgentsFile, true) }

// SkillsEnabled reports whether skill loading is on (default: true).
func (c *Config) SkillsEnabled() bool { return featureEnabled(c.Features.Skills, true) }

// MemoryEnabled reports whether project memory loading is on (default: true).
func (c *Config) MemoryEnabled() bool { return featureEnabled(c.Features.Memory, true) }

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
	if err := validateOpenAIAuth(c.OpenAIAuth, source); err != nil {
		return nil, err
	}
	if c.Model == "" {
		c.Model = defaultModelFor(c.Provider, c.OpenAIAuth)
	}
	if c.Permissions.Mode == "" {
		c.Permissions.Mode = PermissionModeTrusted
	}
	switch c.Permissions.Mode {
	case PermissionModeAsk, PermissionModeTrusted, PermissionModeReadonly:
	default:
		return nil, fmt.Errorf("%s: permissions.mode must be one of %q, %q, %q", source, PermissionModeAsk, PermissionModeTrusted, PermissionModeReadonly)
	}
	return &c, nil
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
