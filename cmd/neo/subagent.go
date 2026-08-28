package main

import (
	"context"
	"fmt"
	"time"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/subagent"
	"github.com/owainlewis/neo/internal/tools"
)

// subagentBackend resolves the optional worker backend. The zero-value config
// keeps the existing behavior: workers follow the coordinator. A configured
// backend stays independent, and credential/setup failures become worker
// failures so the coordinator can report them and continue.
func subagentBackend(ctx context.Context, cfg *config.Config, fallback llm.Provider, fallbackModel string) (llm.Provider, string, bool) {
	if cfg == nil || !cfg.SubagentsConfigured() {
		return fallback, fallbackModel, true
	}
	prov, err := checkedProvider(ctx, cfg, cfg.Subagents.Provider)
	if err != nil {
		prov = unavailableProvider{
			name: cfg.Subagents.Provider,
			err: fmt.Errorf("subagent backend %s/%s is unavailable: %w",
				cfg.Subagents.Provider, cfg.Subagents.Model, err),
		}
	}
	return prov, cfg.Subagents.Model, false
}

type unavailableProvider struct {
	name string
	err  error
}

func (p unavailableProvider) Name() string { return p.name }

func (p unavailableProvider) Complete(context.Context, llm.Request) (*llm.Response, error) {
	return nil, p.err
}

// chatAgentTool builds the agent tool for a coordinator session. Both
// interactive chat and headless `neo run` use it, so they share one
// construction path and one owner of subagent lifecycle and cancellation.
func chatAgentTool(prov llm.Provider, model, cwd, root string, cfg *config.Config) (subagent.AgentTool, <-chan subagent.Event, *subagent.AgentRunner) {
	contextWindowTokens := 0
	if cfg != nil {
		contextWindowTokens = cfg.Compaction.ContextWindowTokens
	}
	runner := &subagent.AgentRunner{
		Provider:            prov,
		DefaultModel:        model,
		ContextWindowTokens: contextWindowTokens,
		Root:                root,
		BashTimeout:         5 * time.Minute,
	}
	sup := subagent.NewSupervisor(runner, subagent.DefaultBudget())
	return subagent.AgentTool{Sup: sup, Dir: cwd}, sup.Events, runner
}

// headlessRegistry builds the tool registry for `neo run`. Headless
// coordinators delegate through the same native agent tool as interactive
// chat: the same supervisor budgets, the same work/inspect capabilities, and
// the same backend selection. The workflow tool stays out, because headless
// runs have no checklist surface.
//
// The agent tool needs a working directory to run subagents in, so a failed
// os.Getwd (empty cwd) falls back to the base registry rather than spawning
// children with nowhere to work. Nothing drains the returned supervisor's
// event channel; Supervisor.attribute drops on a full channel, so headless
// subagent activity never blocks and never reaches stdout.
func headlessRegistry(ctx context.Context, cfg *config.Config, prov llm.Provider, model, cwd, root string) (*tools.Registry, *subagent.AgentRunner) {
	if cwd == "" {
		return newRegistry(cwd, root), nil
	}
	workerProvider, workerModel, _ := subagentBackend(ctx, cfg, prov, model)
	at, _, runner := chatAgentTool(workerProvider, workerModel, cwd, root, cfg)
	return newRegistry(cwd, root, at), runner
}
