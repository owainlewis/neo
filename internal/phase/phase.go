package phase

import (
	"context"
	"fmt"
	"strings"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/tools"
)

type Definition struct {
	Name   string
	Prompt string   // system prompt content (was previously loaded from PromptPath)
	Tools  []string // optional whitelist; empty means inherit registry
	Model  string   // optional override
	Source string   // descriptive origin for error messages (e.g. file path or "embedded:foo.md")
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
	if def.Prompt == "" {
		return nil, fmt.Errorf("phase %s: empty prompt (source: %s)", def.Name, def.Source)
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
		System:   def.Prompt,
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
