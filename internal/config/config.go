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

	defaultModel = "claude-sonnet-4-6"
)

//go:embed defaults/neo.yaml
var embeddedConfigYAML []byte

// Config is the parsed neo.yaml.
type Config struct {
	Model    string   `yaml:"model"`
	Features Features `yaml:"features"`

	// source records where this config was loaded from (a file path or
	// "embedded"); surfaced in diagnostics via Source().
	source string
}

// Features toggles optional, layered capabilities. The core agent loop is never
// gated by a feature — only capabilities built on top of it. Each flag is a
// tri-state *bool: nil ("absent from config") falls back to a built-in default,
// so a minimal neo.yaml still gets the full experience; set a flag to false to
// turn the capability off explicitly.
type Features struct {
	AgentsFile *bool `yaml:"agents_file"` // load AGENTS.md into the chat system prompt
	Skills     *bool `yaml:"skills"`      // discover and expand $name skills
}

// AgentsFileEnabled reports whether AGENTS.md loading is on (default: true).
func (c *Config) AgentsFileEnabled() bool { return featureEnabled(c.Features.AgentsFile, true) }

// SkillsEnabled reports whether skill loading is on (default: true).
func (c *Config) SkillsEnabled() bool { return featureEnabled(c.Features.Skills, true) }

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
	userCfg := filepath.Join(home, userConfigDir, userConfigName)

	for _, path := range []string{projectConfigName, userCfg} {
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

func parseConfig(b []byte, source string) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if c.Model == "" {
		c.Model = defaultModel
	}
	return &c, nil
}

// Source describes where this Config was loaded from (a file path or
// "embedded"). Useful in error messages and diagnostics.
func (c *Config) Source() string { return c.source }
