// Package workflow runs an ordered sequence of phases (a "definition") and
// emits structured events about its progress. It knows nothing about how the
// events are rendered — callers supply a Sink.
package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/artifact"
	"github.com/owainlewis/neo/internal/phase"
)

// Definition is the on-disk shape of a workflow. The fields match what
// internal/flow used to expose; the tag names are kept for YAML compatibility.
type Definition struct {
	Name      string   `yaml:"name"`
	Phases    []string `yaml:"phases"`
	RetryFrom string   `yaml:"retry_from"`
	MaxRounds int      `yaml:"max_rounds"`
}

// LoadDefinition reads and parses a workflow YAML file.
func LoadDefinition(path string) (*Definition, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d Definition
	if err := yaml.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if d.MaxRounds == 0 {
		d.MaxRounds = 3
	}
	return &d, nil
}

// EventKind enumerates the kinds of events the engine emits.
type EventKind string

const (
	WorkflowStarted   EventKind = "workflow_started"
	PhaseStarted      EventKind = "phase_started"
	PhaseCompleted    EventKind = "phase_completed"
	PhaseFailed       EventKind = "phase_failed"
	RoundRetrying     EventKind = "round_retrying"
	WorkflowCompleted EventKind = "workflow_completed"
	WorkflowFailed    EventKind = "workflow_failed"
)

// Event describes a workflow-level state change.
type Event struct {
	Kind    EventKind
	Phase   string // empty for workflow-level events
	Round   int    // 1-based
	Index   int    // 1-based phase index
	Total   int    // total phases
	Message string // failure reason, retry note, etc.
	Output  string // phase output, only set on PhaseCompleted / PhaseFailed
}

// Sink receives engine events. The two channels are kept separate so a sink
// can render workflow structure differently from per-phase agent activity.
type Sink interface {
	// OnWorkflow is called for workflow-level state changes.
	OnWorkflow(Event)
	// OnAgent is called for every agent event inside a running phase, tagged
	// with the phase name. Lets the UI surface fine-grained activity like
	// "running go test ./..." inside the active phase row.
	OnAgent(phase string, e agent.Event)
}

// Engine executes a workflow definition.
type Engine struct {
	// PhasesDir is the directory where phase YAML / prompt files live.
	PhasesDir string
	// Runner runs an individual phase. The engine takes exclusive ownership
	// of Runner.OnEvent for the duration of Run.
	Runner *phase.Runner
	// Store receives per-phase artifacts.
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
	runID := fmt.Sprintf("%s-%d", def.Name, time.Now().Unix())
	if err := e.Store.InitRun(runID); err != nil {
		return err
	}

	total := len(def.Phases)
	retryStart := 0
	if def.RetryFrom != "" {
		for i, p := range def.Phases {
			if p == def.RetryFrom {
				retryStart = i
				break
			}
		}
	}

	maxRounds := def.MaxRounds
	if maxRounds < 1 {
		maxRounds = 1
	}

	// Take exclusive control of the phase runner's event handler for the
	// duration of this run, restoring the original on exit. Agent events from
	// the active phase are routed through Sink.OnAgent.
	prevOnEvent := e.Runner.OnEvent
	e.Runner.OnEvent = func(phaseName string, ev agent.Event) {
		if e.Sink != nil {
			e.Sink.OnAgent(phaseName, ev)
		}
	}
	defer func() { e.Runner.OnEvent = prevOnEvent }()

	e.emit(Event{Kind: WorkflowStarted, Round: 1, Total: total})

	artifacts := map[string]string{}
	for round := 1; round <= maxRounds; round++ {
		start := 0
		if round > 1 {
			start = retryStart
		}

		failed := false
		for i := start; i < total; i++ {
			name := def.Phases[i]
			e.emit(Event{Kind: PhaseStarted, Phase: name, Round: round, Index: i + 1, Total: total})

			pdef, err := e.loadPhase(name)
			if err != nil {
				msg := err.Error()
				e.emit(Event{Kind: PhaseFailed, Phase: name, Round: round, Index: i + 1, Total: total, Message: msg})
				e.emit(Event{Kind: WorkflowFailed, Message: msg})
				return err
			}

			result, err := e.Runner.Run(ctx, pdef, phase.Input{Task: task, Artifacts: artifacts})
			if err != nil {
				msg := err.Error()
				e.emit(Event{Kind: PhaseFailed, Phase: name, Round: round, Index: i + 1, Total: total, Message: msg})
				e.emit(Event{Kind: WorkflowFailed, Message: msg})
				return err
			}

			artifacts[name] = result.Output
			_ = e.Store.WritePhase(runID, name, round, result.Output)

			if failsHeuristic(result.Output) {
				failed = true
				e.emit(Event{Kind: PhaseFailed, Phase: name, Round: round, Index: i + 1, Total: total, Message: "phase reports failure", Output: result.Output})
				if def.RetryFrom != "" && round < maxRounds {
					e.emit(Event{Kind: RoundRetrying, Round: round + 1, Total: total, Message: "retrying from " + def.RetryFrom})
					break
				}
				msg := fmt.Sprintf("phase %s failed (round %d)", name, round)
				e.emit(Event{Kind: WorkflowFailed, Message: msg})
				return fmt.Errorf("%s", msg)
			}

			e.emit(Event{Kind: PhaseCompleted, Phase: name, Round: round, Index: i + 1, Total: total, Output: result.Output})
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

// loadPhase resolves a phase by name from PhasesDir. Looks for <name>.yaml
// first, then falls back to <name>.md (a bare prompt with no overrides).
func (e *Engine) loadPhase(name string) (phase.Definition, error) {
	yamlPath := filepath.Join(e.PhasesDir, name+".yaml")
	b, err := os.ReadFile(yamlPath)
	if err != nil {
		mdPath := filepath.Join(e.PhasesDir, name+".md")
		if _, err2 := os.Stat(mdPath); err2 == nil {
			return phase.Definition{Name: name, PromptPath: mdPath}, nil
		}
		return phase.Definition{}, err
	}
	var d phase.Definition
	if err := yaml.Unmarshal(b, &d); err != nil {
		return d, err
	}
	if d.Name == "" {
		d.Name = name
	}
	if d.PromptPath == "" {
		d.PromptPath = filepath.Join(e.PhasesDir, name+".md")
	} else if !filepath.IsAbs(d.PromptPath) {
		d.PromptPath = filepath.Join(e.PhasesDir, d.PromptPath)
	}
	return d, nil
}
