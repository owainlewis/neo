package phase

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/tools"
)

type Definition struct {
	Name       string   `yaml:"name"`
	PromptPath string   `yaml:"prompt"`
	Tools      []string `yaml:"tools"`
	Model      string   `yaml:"model"`
}

type Input struct {
	Task      string
	Artifacts map[string]string
}

type Result struct {
	Name       string
	Output     string
	Transcript []llm.Message
}

type Runner struct {
	Provider     llm.Provider
	Tools        *tools.Registry
	DefaultModel string
	OnEvent      func(string, agent.Event)
}

func (r *Runner) Run(ctx context.Context, def Definition, in Input) (*Result, error) {
	prompt, err := os.ReadFile(def.PromptPath)
	if err != nil {
		return nil, fmt.Errorf("read phase prompt %s: %w", def.PromptPath, err)
	}
	model := def.Model
	if model == "" {
		model = r.DefaultModel
	}

	var b strings.Builder
	b.WriteString("# Task\n")
	b.WriteString(in.Task)
	if len(in.Artifacts) > 0 {
		b.WriteString("\n\n# Artifacts from prior phases\n")
		for name, content := range in.Artifacts {
			b.WriteString(fmt.Sprintf("\n## %s\n%s\n", name, content))
		}
	}
	b.WriteString("\n\nBegin.")

	ag := agent.New(agent.Config{
		Model:    model,
		System:   string(prompt),
		Provider: r.Provider,
		Tools:    r.Tools.Filter(def.Tools),
		OnEvent: func(e agent.Event) {
			if r.OnEvent != nil {
				r.OnEvent(def.Name, e)
			}
		},
	})

	out, err := ag.Send(ctx, b.String())
	if err != nil {
		return nil, err
	}
	return &Result{Name: def.Name, Output: out, Transcript: ag.Transcript()}, nil
}
