package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/factory"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/permission"
)

// chatAgentTool builds the agent tool for an interactive chat session: the
// chat agent is caller node 0, so every subagent it spawns becomes a root of
// the supervisor's tree. Events tee to .neo/events.jsonl and to the returned
// channel for the TUI's live subagent tree.
func chatAgentTool(prov llm.Provider, model string, cfg *config.Config, cwd, root string) (factory.AgentTool, <-chan factory.Event, *factory.AgentRunner) {
	runner := &factory.AgentRunner{
		Provider:     prov,
		DefaultModel: model,
		Root:         root,
		BashTimeout:  5 * time.Minute,
		// A readonly session must not gain write access by delegating;
		// ask-mode sessions gate at the agent-tool approval instead. Subagents
		// run autonomously once approved. Mode is fixed at session start.
		Mode: permission.Mode(cfg.Permissions.Mode),
	}
	sup := factory.NewSupervisor(runner, factory.DefaultBudget(), factory.Resolver{})
	runner.Sup = sup
	ui := make(chan factory.Event, 256)
	go teeEvents(sup.Events, filepath.Join(root, ".neo", "events.jsonl"), ui)
	return factory.AgentTool{Sup: sup, CallerNode: 0, Dir: cwd}, ui, runner
}

// teeEvents drains an event stream to a jsonl file and, when forward is
// non-nil, fans events out to it without ever blocking — a slow or absent
// UI drops frames; it never stalls agents. Best-effort on the file: if it
// can't be opened the stream is still drained so the supervisor's buffer
// never fills.
func teeEvents(events <-chan factory.Event, path string, forward chan<- factory.Event) {
	enc := json.NewEncoder(io.Discard)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			defer f.Close()
			enc = json.NewEncoder(f)
		}
	}
	for ev := range events {
		_ = enc.Encode(ev)
		if forward != nil {
			select {
			case forward <- ev:
			default:
			}
		}
	}
	if forward != nil {
		close(forward)
	}
}
