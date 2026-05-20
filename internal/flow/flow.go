// Package flow is a backwards-compatibility shim around internal/workflow.
//
// New code should use internal/workflow directly. This package preserves the
// existing line-printer-shaped API (StatusUpdate / OnStatus / OnEvent) so the
// `neo flow` CLI keeps working unchanged while the rest of the codebase
// migrates. See GH #22.
package flow

import (
	"context"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/artifact"
	"github.com/owainlewis/neo/internal/phase"
	"github.com/owainlewis/neo/internal/workflow"
)

// Definition mirrors workflow.Definition; alias rather than rewrap.
type Definition = workflow.Definition

// Status enumerates the legacy line-printer status codes.
type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in-progress"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusRetrying   Status = "retrying"
)

// StatusUpdate is the message shape the line printer (internal/ui) consumes.
type StatusUpdate struct {
	Phase   string
	Index   int
	Total   int
	Round   int
	Status  Status
	Message string
}

// LoadDefinition reads a workflow YAML file.
func LoadDefinition(path string) (*Definition, error) {
	return workflow.LoadDefinition(path)
}

// Runner is the legacy facade. New code should use workflow.Engine directly.
type Runner struct {
	PhasesDir string
	Runner    *phase.Runner
	Store     *artifact.Store
	OnStatus  func(StatusUpdate)
	OnEvent   func(string, agent.Event)
}

// Run executes the workflow via the new engine, translating events back into
// the legacy StatusUpdate shape.
func (r *Runner) Run(ctx context.Context, def Definition, task string) error {
	sink := &legacySink{
		onStatus: r.OnStatus,
		onEvent:  r.OnEvent,
	}
	eng := &workflow.Engine{
		PhasesDir: r.PhasesDir,
		Runner:    r.Runner,
		Store:     r.Store,
		Sink:      sink,
	}
	return eng.Run(ctx, def, task)
}

// legacySink converts workflow events into the legacy StatusUpdate / OnEvent
// callbacks that the line printer expects.
type legacySink struct {
	onStatus func(StatusUpdate)
	onEvent  func(string, agent.Event)

	// lastFailedPhase remembers which phase emitted PhaseFailed so we can
	// re-issue it as StatusRetrying when a RoundRetrying event follows.
	lastFailedPhase string
	lastFailedIndex int
	lastFailedRound int
	lastFailedTotal int
}

func (s *legacySink) OnWorkflow(e workflow.Event) {
	if s.onStatus == nil {
		return
	}
	switch e.Kind {
	case workflow.PhaseStarted:
		s.onStatus(StatusUpdate{
			Phase: e.Phase, Index: e.Index, Total: e.Total,
			Round: e.Round, Status: StatusInProgress,
		})
	case workflow.PhaseCompleted:
		s.onStatus(StatusUpdate{
			Phase: e.Phase, Index: e.Index, Total: e.Total,
			Round: e.Round, Status: StatusCompleted,
		})
	case workflow.PhaseFailed:
		s.onStatus(StatusUpdate{
			Phase: e.Phase, Index: e.Index, Total: e.Total,
			Round: e.Round, Status: StatusFailed, Message: e.Message,
		})
		s.lastFailedPhase = e.Phase
		s.lastFailedIndex = e.Index
		s.lastFailedRound = e.Round
		s.lastFailedTotal = e.Total
	case workflow.RoundRetrying:
		if s.lastFailedPhase != "" {
			s.onStatus(StatusUpdate{
				Phase: s.lastFailedPhase, Index: s.lastFailedIndex, Total: s.lastFailedTotal,
				Round: s.lastFailedRound, Status: StatusRetrying, Message: e.Message,
			})
			s.lastFailedPhase = ""
		}
	}
}

func (s *legacySink) OnAgent(phase string, ev agent.Event) {
	if s.onEvent != nil {
		s.onEvent(phase, ev)
	}
}
