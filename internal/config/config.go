// Package config loads neo's single configuration file (neo.yaml /
// ~/.neo/config.yaml / embedded default) and resolves step prompts by name.
package config

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/owainlewis/neo/internal/phase"
)

// Resolution paths and embedded fallback assets.
const (
	projectConfigName = "neo.yaml"
	userConfigDir     = ".neo"
	userConfigName    = "config.yaml"

	projectFlowsDir = "flows"
	userFlowsDir    = "flows"

	defaultModel        = "claude-sonnet-4-6"
	defaultArtifactsDir = ".agent/runs"
)

//go:embed defaults/neo.yaml
var embeddedConfigYAML []byte

//go:embed defaults/flows/*.md
var embeddedFlows embed.FS

// Config is the parsed neo.yaml.
type Config struct {
	Model        string                `yaml:"model"`
	ArtifactsDir string                `yaml:"artifacts_dir"`
	Flows        map[string]FlowConfig `yaml:"flows"`

	// Internal — where this config came from and which directories to
	// search for step prompts. Set by Load and used by ResolveStep.
	source     string
	stepDirs   []string // project flows/ → user ~/.neo/flows/
	homeFlows  string   // for diagnostics
}

// FlowConfig is one named flow's orchestration spec.
type FlowConfig struct {
	Steps     []string `yaml:"steps"`
	RetryFrom string   `yaml:"retry_from"`
	MaxRounds int      `yaml:"max_rounds"`
}

// Load reads the first available config file:
//
//	./neo.yaml → ~/.neo/config.yaml → embedded default
//
// First hit wins — no merging. The returned *Config also knows where to
// search for step prompts via ResolveStep.
func Load() (*Config, error) {
	home, _ := os.UserHomeDir()
	userCfg := filepath.Join(home, userConfigDir, userConfigName)
	userFlows := filepath.Join(home, userConfigDir, userFlowsDir)

	stepDirs := []string{projectFlowsDir, userFlows}

	candidates := []string{projectConfigName, userCfg}
	for _, path := range candidates {
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
		cfg.stepDirs = stepDirs
		cfg.homeFlows = userFlows
		return cfg, nil
	}

	cfg, err := parseConfig(embeddedConfigYAML, "embedded:neo.yaml")
	if err != nil {
		return nil, err
	}
	cfg.source = "embedded"
	cfg.stepDirs = stepDirs
	cfg.homeFlows = userFlows
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
	if c.ArtifactsDir == "" {
		c.ArtifactsDir = defaultArtifactsDir
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return &c, nil
}

// Validate runs eager structural checks. Called by Load; safe to call
// directly from tests or a `neo config check` command.
func (c *Config) Validate() error {
	for name, f := range c.Flows {
		if len(f.Steps) == 0 {
			return fmt.Errorf("flows.%s: must define non-empty steps", name)
		}
		if f.RetryFrom != "" {
			found := false
			for _, s := range f.Steps {
				if s == f.RetryFrom {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("flows.%s.retry_from: %q is not in steps %v", name, f.RetryFrom, f.Steps)
			}
		}
		if f.MaxRounds < 0 {
			return fmt.Errorf("flows.%s.max_rounds: must be >= 0, got %d", name, f.MaxRounds)
		}
	}
	return nil
}

// Source describes where this Config was loaded from (a file path or
// "embedded"). Useful in error messages and `/flows` output.
func (c *Config) Source() string { return c.source }

// FlowNames returns the names of all defined flows in deterministic order.
func (c *Config) FlowNames() []string {
	names := make([]string, 0, len(c.Flows))
	for n := range c.Flows {
		names = append(names, n)
	}
	// Sort for stable UI output.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	return names
}

// StepNotFoundError is returned by ResolveStep when no candidate path nor
// embedded asset matches the name. The error lists every location tried so
// the user can see exactly where to add the missing file.
type StepNotFoundError struct {
	Name      string
	Searched  []string
}

func (e *StepNotFoundError) Error() string {
	return fmt.Sprintf("step %q not found: looked in %v", e.Name, e.Searched)
}

// ResolveStep loads a step's prompt by name. Looks in project flows/ →
// user ~/.neo/flows/ → embedded defaults; first hit wins. Parses optional
// YAML frontmatter for per-step tool / model overrides.
func (c *Config) ResolveStep(name string) (phase.Definition, error) {
	file := name + ".md"

	searched := make([]string, 0, len(c.stepDirs)+1)
	for _, dir := range c.stepDirs {
		path := filepath.Join(dir, file)
		searched = append(searched, path)
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return phase.Definition{}, fmt.Errorf("read %s: %w", path, err)
		}
		return parseStep(name, b, path)
	}

	embeddedPath := "defaults/flows/" + file
	searched = append(searched, "embedded:"+embeddedPath)
	b, err := embeddedFlows.ReadFile(embeddedPath)
	if err != nil {
		if errIsNotExist(err) {
			return phase.Definition{}, &StepNotFoundError{Name: name, Searched: searched}
		}
		return phase.Definition{}, fmt.Errorf("read embedded %s: %w", embeddedPath, err)
	}
	return parseStep(name, b, "embedded:"+file)
}

func errIsNotExist(err error) bool {
	if err == nil {
		return false
	}
	var pathErr *fs.PathError
	if as := errors_As(err, &pathErr); as {
		return os.IsNotExist(pathErr.Err)
	}
	return os.IsNotExist(err)
}

// errors_As avoids importing the errors package solely for As, keeping the
// import list short. Local fallback only used by errIsNotExist.
func errors_As(err error, target any) bool {
	pe, ok := target.(**fs.PathError)
	if !ok {
		return false
	}
	for err != nil {
		if v, ok := err.(*fs.PathError); ok {
			*pe = v
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// parseStep extracts optional YAML frontmatter for tool / model overrides
// and returns a phase.Definition. The prompt body is everything after the
// frontmatter (or the whole file when no frontmatter is present).
//
// Frontmatter format (optional):
//
//	---
//	tools: [read_file, bash]
//	model: claude-haiku-4-5
//	---
//
//	You are the REVIEW step...
func parseStep(name string, content []byte, source string) (phase.Definition, error) {
	def := phase.Definition{Name: name, Source: source}
	body := content

	const marker = "---\n"
	if bytes.HasPrefix(content, []byte(marker)) {
		rest := content[len(marker):]
		end := bytes.Index(rest, []byte("\n---\n"))
		if end < 0 {
			// Tolerate trailing "---" without newline at EOF.
			if idx := bytes.LastIndex(rest, []byte("\n---")); idx >= 0 && idx+4 >= len(rest)-1 {
				end = idx
			} else {
				return phase.Definition{}, fmt.Errorf("%s: unterminated frontmatter (missing closing ---)", source)
			}
		}
		fm := rest[:end]
		body = rest[end+len("\n---\n"):]

		var meta struct {
			Tools []string `yaml:"tools"`
			Model string   `yaml:"model"`
		}
		if err := yaml.Unmarshal(fm, &meta); err != nil {
			return phase.Definition{}, fmt.Errorf("%s: frontmatter: %w", source, err)
		}
		def.Tools = meta.Tools
		def.Model = meta.Model
	}

	def.Prompt = string(bytes.TrimSpace(body))
	if def.Prompt == "" {
		return phase.Definition{}, fmt.Errorf("%s: empty prompt", source)
	}
	return def, nil
}
