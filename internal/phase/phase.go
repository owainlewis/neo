package phase

import (
	"context"
	"fmt"
	"strings"
	"text/template"

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

// StepRef is the read-only summary of a step execution that other steps in
// the same workflow can reference via the template context.
type StepRef struct {
	Name   string
	Output string
	Round  int
}

// Input is the per-step runtime context passed to Runner.Run. The Task is
// the original workflow task; Round, Prev and Steps form the template
// context surfaced to the step's prompt body.
type Input struct {
	Task  string
	Round int                 // 1-based
	Prev  *StepRef            // nil for the very first step of the workflow
	Steps map[string]*StepRef // most recent output by step name (cross-reference)
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

// templateContext is the value passed to text/template execution. Keep this
// in sync with the documented variables (.Task, .Round, .Prev, .Steps).
type templateContext struct {
	Task  string
	Round int
	Prev  *StepRef
	Steps map[string]*StepRef
}

func (r *Runner) Run(ctx context.Context, def Definition, in Input) (*Result, error) {
	if def.Prompt == "" {
		return nil, fmt.Errorf("phase %s: empty prompt (source: %s)", def.Name, def.Source)
	}
	model := def.Model
	if model == "" {
		model = r.DefaultModel
	}

	// Render the step prompt as a text/template with workflow context. A
	// prompt that doesn't use {{ … }} markers renders unchanged, so plain
	// prompts keep working.
	system, err := renderPrompt(def, in)
	if err != nil {
		return nil, err
	}

	// User message intentionally stays minimal — the step body (via
	// templates) owns presentation of prior context. The engine no longer
	// injects an "Artifacts from prior phases" block.
	var b strings.Builder
	b.WriteString("# Task\n")
	b.WriteString(in.Task)
	b.WriteString("\n\nBegin.")

	ag := agent.New(agent.Config{
		Model:    model,
		System:   system,
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

func renderPrompt(def Definition, in Input) (string, error) {
	tmpl, err := template.New(def.Name).Parse(def.Prompt)
	if err != nil {
		return "", fmt.Errorf("step %q (%s): template parse: %w", def.Name, def.Source, err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, templateContext{
		Task:  in.Task,
		Round: in.Round,
		Prev:  in.Prev,
		Steps: in.Steps,
	}); err != nil {
		return "", fmt.Errorf("step %q (%s): template execute: %w", def.Name, def.Source, err)
	}
	return buf.String(), nil
}
