package main

import (
	"context"
	"fmt"
	"time"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/subagent"
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

// chatAgentTool builds the agent tool for an interactive chat session.
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
