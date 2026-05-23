package workflow

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type flowFile struct {
	Steps []flowFileStep `yaml:"steps"`
}

type flowFileStep struct {
	Name   string   `yaml:"name"`
	Type   string   `yaml:"type"`
	Prompt string   `yaml:"prompt"`
	Run    string   `yaml:"run"`
	Tools  []string `yaml:"tools"`
	Model  string   `yaml:"model"`
}

// LoadFile reads the simple v1 workflow format: an ordered list of agent and
// command steps. Agent prompts are Markdown files; command steps are shell
// strings. Relative prompt paths are resolved from the flow file directory.
func LoadFile(path string) (Definition, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, fmt.Errorf("read flow %s: %w", path, err)
	}

	var f flowFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		return Definition{}, fmt.Errorf("%s: %w", path, err)
	}
	if len(f.Steps) == 0 {
		return Definition{}, fmt.Errorf("%s: must define non-empty steps", path)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	base := filepath.Dir(abs)
	def := Definition{
		Name:      filepath.Base(path),
		MaxRounds: 1,
		FlowPath:  abs,
		StepDefs:  make([]StepDefinition, 0, len(f.Steps)),
	}

	for i, raw := range f.Steps {
		step, err := loadFileStep(base, abs, i, raw)
		if err != nil {
			return Definition{}, err
		}
		def.StepDefs = append(def.StepDefs, step)
	}
	return def, nil
}

func loadFileStep(base, flowPath string, index int, raw flowFileStep) (StepDefinition, error) {
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		return StepDefinition{}, fmt.Errorf("%s: steps[%d].name is required", flowPath, index)
	}

	kind := StepKind(strings.ToLower(strings.TrimSpace(raw.Type)))
	if kind == "" {
		switch {
		case raw.Run != "":
			kind = StepCommand
		case raw.Prompt != "":
			kind = StepAgent
		}
	}

	step := StepDefinition{
		Name:   name,
		Kind:   kind,
		Run:    raw.Run,
		Tools:  raw.Tools,
		Model:  raw.Model,
		Source: flowPath,
	}
	switch kind {
	case StepAgent:
		if strings.TrimSpace(raw.Prompt) == "" {
			return StepDefinition{}, fmt.Errorf("%s: agent step %q requires prompt", flowPath, name)
		}
		promptPath := raw.Prompt
		if !filepath.IsAbs(promptPath) {
			promptPath = filepath.Join(base, promptPath)
		}
		b, err := os.ReadFile(promptPath)
		if err != nil {
			return StepDefinition{}, fmt.Errorf("read prompt for step %q (%s): %w", name, promptPath, err)
		}
		step.Prompt = string(bytes.TrimSpace(b))
		step.Source = promptPath
	case StepCommand:
		if strings.TrimSpace(raw.Run) == "" {
			return StepDefinition{}, fmt.Errorf("%s: command step %q requires run", flowPath, name)
		}
	default:
		return StepDefinition{}, fmt.Errorf("%s: step %q has unknown type %q", flowPath, name, raw.Type)
	}
	return step, nil
}

func LooksLikeFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml" || strings.ContainsAny(path, `/\`)
}
