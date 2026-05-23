// Package workflow runs an ordered sequence of steps (a "definition") and
// emits structured events about its progress. It knows nothing about how the
// events are rendered or where step prompts live — callers supply a Sink for
// observation and a StepResolver for prompt lookup.
package workflow

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/artifact"
	"github.com/owainlewis/neo/internal/phase"
)

// Definition is what the engine executes: a named, ordered sequence of step
// names plus retry policy. Constructed by the caller (typically from a
// loaded neo.yaml config), not parsed from disk by this package any more.
type Definition struct {
	Name      string
	Steps     []string
	RetryFrom string
	MaxRounds int
	FlowPath  string
	StepDefs  []StepDefinition
}

// StepKind is the tiny v1 workflow vocabulary: run an agent prompt or run a
// shell command gate.
type StepKind string

const (
	StepAgent   StepKind = "agent"
	StepCommand StepKind = "command"
)

// StepDefinition is a concrete step loaded from a flow YAML file.
type StepDefinition struct {
	Name   string
	Kind   StepKind
	Prompt string
	Run    string
	Tools  []string
	Model  string
	Source string
}

// StepNames returns the user-facing sequence for rendering and progress.
func (d Definition) StepNames() []string {
	if len(d.StepDefs) == 0 {
		return d.Steps
	}
	out := make([]string, 0, len(d.StepDefs))
	for _, step := range d.StepDefs {
		out = append(out, step.Name)
	}
	return out
}

// StepResolver returns the prompt + per-step settings for a named step.
// Implemented by internal/config; the engine itself never touches the
// filesystem to find steps.
type StepResolver interface {
	ResolveStep(name string) (phase.Definition, error)
}

// EventKind enumerates the kinds of events the engine emits.
type EventKind string

const (
	WorkflowStarted   EventKind = "workflow_started"
	StepStarted       EventKind = "step_started"
	StepCompleted     EventKind = "step_completed"
	StepFailed        EventKind = "step_failed"
	RoundRetrying     EventKind = "round_retrying"
	WorkflowCompleted EventKind = "workflow_completed"
	WorkflowFailed    EventKind = "workflow_failed"
)

// Event describes a workflow-level state change. Step carries the step name
// for per-step events; on RoundRetrying it carries the retry-from step so
// sinks can reset every downstream row before the new round runs.
type Event struct {
	Kind    EventKind
	Step    string // empty for workflow-level events
	Round   int    // 1-based
	Index   int    // 1-based step index
	Total   int    // total steps in the flow
	Message string // failure reason, retry note, etc.
	Output  string // step output, only set on StepCompleted / StepFailed
}

// Sink receives engine events. OnWorkflow handles structural transitions;
// OnAgent surfaces fine-grained agent activity inside the running step so
// the UI can populate a detail line (e.g. "running go test ./...").
type Sink interface {
	OnWorkflow(Event)
	OnAgent(step string, e agent.Event)
}

// Engine executes a workflow definition.
type Engine struct {
	// Resolver loads step prompts by name. Typically a *config.Config.
	Resolver StepResolver
	// Runner runs an individual step's agent. The engine takes exclusive
	// ownership of Runner.OnEvent for the duration of Run, restoring it
	// on exit.
	Runner *phase.Runner
	// Store receives per-step artifacts.
	Store *artifact.Store
	// Sink, if non-nil, receives workflow and agent events.
	Sink Sink
}

func (e *Engine) emit(ev Event) {
	if e.Sink != nil {
		e.Sink.OnWorkflow(ev)
	}
}

// Run executes the workflow. It blocks until the workflow finishes, fails, or
// ctx is cancelled. The returned error is non-nil iff the workflow ended in a
// terminal failure state (matched by a WorkflowFailed event).
func (e *Engine) Run(ctx context.Context, def Definition, task string) error {
	runID := fmt.Sprintf("%s-%d", slug(def.Name), time.Now().Unix())
	if err := e.Store.InitRun(runID); err != nil {
		return err
	}

	cwd, _ := os.Getwd()
	stepNames := def.StepNames()
	total := len(stepNames)
	retryStart := 0
	if def.RetryFrom != "" {
		for i, s := range stepNames {
			if s == def.RetryFrom {
				retryStart = i
				break
			}
		}
	}

	maxRounds := def.MaxRounds
	if maxRounds < 1 {
		maxRounds = 1
	}

	// Take exclusive control of the step runner's event handler for the
	// duration of this run, restoring the original on exit. Agent events
	// from the active step are routed through Sink.OnAgent.
	prevOnEvent := e.Runner.OnEvent
	e.Runner.OnEvent = func(stepName string, ev agent.Event) {
		if e.Sink != nil {
			e.Sink.OnAgent(stepName, ev)
		}
	}
	defer func() { e.Runner.OnEvent = prevOnEvent }()

	e.emit(Event{Kind: WorkflowStarted, Round: 1, Total: total})

	// Template context that's carried across rounds. `prev` is the most
	// recently completed step (used as `.Prev` by the next step's template);
	// `steps` holds the most recent output per step name (used as
	// `.Steps[name]` for cross-reference). Both persist across rounds.
	var prev *phase.StepRef
	steps := map[string]*phase.StepRef{}

	for round := 1; round <= maxRounds; round++ {
		start := 0
		if round > 1 {
			start = retryStart
		}

		failed := false
		for i := start; i < total; i++ {
			name := stepNames[i]
			e.emit(Event{Kind: StepStarted, Step: name, Round: round, Index: i + 1, Total: total})

			output, err := e.runStep(ctx, def, i, name, task, round, prev, steps, runID, cwd)
			if err != nil {
				msg := err.Error()
				if output != "" {
					_ = e.Store.WritePhase(runID, name, round, output)
				}
				e.emit(Event{Kind: StepFailed, Step: name, Round: round, Index: i + 1, Total: total, Message: msg, Output: output})
				e.emit(Event{Kind: WorkflowFailed, Message: msg})
				return err
			}

			ref := &phase.StepRef{Name: name, Output: output, Round: round}
			steps[name] = ref
			prev = ref
			if err := e.Store.WritePhase(runID, name, round, output); err != nil {
				msg := err.Error()
				e.emit(Event{Kind: StepFailed, Step: name, Round: round, Index: i + 1, Total: total, Message: msg})
				e.emit(Event{Kind: WorkflowFailed, Message: msg})
				return err
			}

			if len(def.StepDefs) == 0 && failsHeuristic(output) {
				failed = true
				e.emit(Event{Kind: StepFailed, Step: name, Round: round, Index: i + 1, Total: total, Message: "step reports failure", Output: output})
				if def.RetryFrom != "" && round < maxRounds {
					// Step carries the retry-from name so a sink can reset
					// all rows from that index onward for the next round,
					// not just the one that failed.
					e.emit(Event{Kind: RoundRetrying, Step: def.RetryFrom, Round: round + 1, Total: total, Message: "retrying from " + def.RetryFrom})
					break
				}
				msg := fmt.Sprintf("step %s failed (round %d)", name, round)
				e.emit(Event{Kind: WorkflowFailed, Message: msg})
				return fmt.Errorf("%s", msg)
			}

			e.emit(Event{Kind: StepCompleted, Step: name, Round: round, Index: i + 1, Total: total, Output: output})
		}

		if !failed {
			e.emit(Event{Kind: WorkflowCompleted})
			return nil
		}
	}

	msg := fmt.Sprintf("max rounds (%d) reached", maxRounds)
	e.emit(Event{Kind: WorkflowFailed, Message: msg})
	return fmt.Errorf("%s", msg)
}

func (e *Engine) runStep(ctx context.Context, def Definition, idx int, name, task string, round int, prev *phase.StepRef, steps map[string]*phase.StepRef, runID, cwd string) (string, error) {
	in := phase.Input{
		Task:     task,
		Round:    round,
		Prev:     prev,
		Steps:    steps,
		RunID:    runID,
		CWD:      cwd,
		FlowPath: def.FlowPath,
		StepName: name,
	}

	if len(def.StepDefs) == 0 {
		pdef, err := e.Resolver.ResolveStep(name)
		if err != nil {
			return "", err
		}
		result, err := e.Runner.Run(ctx, pdef, in)
		if err != nil {
			return "", err
		}
		return result.Output, nil
	}

	step := def.StepDefs[idx]
	switch step.Kind {
	case StepAgent:
		result, err := e.Runner.Run(ctx, phase.Definition{
			Name:   step.Name,
			Prompt: step.Prompt,
			Tools:  step.Tools,
			Model:  step.Model,
			Source: step.Source,
		}, in)
		if err != nil {
			return "", err
		}
		return result.Output, nil
	case StepCommand:
		cmd, err := phase.RenderText(step.Name, step.Source, step.Run, in)
		if err != nil {
			return "", err
		}
		return e.runCommandStep(ctx, step.Name, cmd, cwd)
	default:
		return "", fmt.Errorf("step %q: unknown type %q", step.Name, step.Kind)
	}
}

func (e *Engine) runCommandStep(ctx context.Context, name, command, cwd string) (string, error) {
	if e.Sink != nil {
		e.Sink.OnAgent(name, agent.Event{Kind: agent.EventToolCall, Name: "bash", Args: map[string]any{"command": command}})
	}

	started := time.Now()
	c := exec.CommandContext(ctx, "/bin/bash", "-lc", command)
	c.Dir = cwd
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	out := formatCommandOutput(command, time.Since(started), stdout.String(), stderr.String())

	isErr := err != nil
	if e.Sink != nil {
		e.Sink.OnAgent(name, agent.Event{Kind: agent.EventToolResult, Name: "bash", Text: out, IsError: isErr})
	}
	if err != nil {
		if ctx.Err() != nil {
			return out, fmt.Errorf("command cancelled: %w", ctx.Err())
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return out, fmt.Errorf("command failed with exit %d", ee.ExitCode())
		}
		return out, err
	}
	return out, nil
}

func formatCommandOutput(command string, d time.Duration, stdout, stderr string) string {
	var b strings.Builder
	b.WriteString("$ ")
	b.WriteString(command)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("duration: %s\n", d.Round(time.Millisecond)))
	if stdout != "" {
		b.WriteString("\nstdout:\n")
		b.WriteString(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			b.WriteString("\n")
		}
	}
	if stderr != "" {
		b.WriteString("\nstderr:\n")
		b.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func slug(s string) string {
	s = filepath.Base(s)
	s = strings.ToLower(s)
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "flow"
	}
	return out
}
