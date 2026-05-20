package flow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/artifact"
	"github.com/owainlewis/neo/internal/phase"
)

type Definition struct {
	Name      string   `yaml:"name"`
	Phases    []string `yaml:"phases"`
	RetryFrom string   `yaml:"retry_from"`
	MaxRounds int      `yaml:"max_rounds"`
}

type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in-progress"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusRetrying   Status = "retrying"
)

type StatusUpdate struct {
	Phase     string
	Index     int
	Total     int
	Round     int
	Status    Status
	Message   string
}

type Runner struct {
	PhasesDir string
	Runner    *phase.Runner
	Store     *artifact.Store
	OnStatus  func(StatusUpdate)
	OnEvent   func(string, agent.Event)
}

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

func (r *Runner) loadPhase(name string) (phase.Definition, error) {
	path := filepath.Join(r.PhasesDir, name+".yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		mdPath := filepath.Join(r.PhasesDir, name+".md")
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
		d.PromptPath = filepath.Join(r.PhasesDir, name+".md")
	} else if !filepath.IsAbs(d.PromptPath) {
		d.PromptPath = filepath.Join(r.PhasesDir, d.PromptPath)
	}
	return d, nil
}

func (r *Runner) status(u StatusUpdate) {
	if r.OnStatus != nil {
		r.OnStatus(u)
	}
}

func (r *Runner) Run(ctx context.Context, def Definition, task string) error {
	runID := fmt.Sprintf("%s-%d", def.Name, time.Now().Unix())
	if err := r.Store.InitRun(runID); err != nil {
		return err
	}

	artifacts := map[string]string{}
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

	for round := 1; round <= def.MaxRounds; round++ {
		start := 0
		if round > 1 {
			start = retryStart
		}

		failed := false
		for i := start; i < total; i++ {
			name := def.Phases[i]
			r.status(StatusUpdate{Phase: name, Index: i + 1, Total: total, Round: round, Status: StatusInProgress})

			pdef, err := r.loadPhase(name)
			if err != nil {
				r.status(StatusUpdate{Phase: name, Index: i + 1, Total: total, Round: round, Status: StatusFailed, Message: err.Error()})
				return err
			}

			result, err := r.Runner.Run(ctx, pdef, phase.Input{Task: task, Artifacts: artifacts})
			if err != nil {
				r.status(StatusUpdate{Phase: name, Index: i + 1, Total: total, Round: round, Status: StatusFailed, Message: err.Error()})
				return err
			}

			artifacts[name] = result.Output
			_ = r.Store.WritePhase(runID, name, round, result.Output)

			if failsHeuristic(result.Output) {
				failed = true
				r.status(StatusUpdate{Phase: name, Index: i + 1, Total: total, Round: round, Status: StatusFailed, Message: "phase reports failure"})
				if def.RetryFrom != "" && round < def.MaxRounds {
					r.status(StatusUpdate{Phase: name, Index: i + 1, Total: total, Round: round, Status: StatusRetrying})
					break
				}
				return fmt.Errorf("phase %s failed (round %d)", name, round)
			}

			r.status(StatusUpdate{Phase: name, Index: i + 1, Total: total, Round: round, Status: StatusCompleted})
		}

		if !failed {
			return nil
		}
	}
	return fmt.Errorf("max rounds (%d) reached", def.MaxRounds)
}

func failsHeuristic(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range []string{"verdict: fail", "status: fail", "result: fail", "❌", "blocking issues:", "tests failed"} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}
