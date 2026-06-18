package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/factory"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/workspace"
)

// runStepCmd drives `neo step <name> "<input>"` — run one step in isolation,
// under the same supervisor (so nested run_step calls work), printing the
// final output. The fast path for iterating on step prompts.
func runStepCmd(ctx context.Context, args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, `usage: neo step <name> "<input>"`)
		os.Exit(2)
	}
	budget := factory.DefaultBudget()
	out, err := runSupervised(ctx, budget, args[0], args[1])
	fmt.Println(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// chatRunStepTool builds the run_step tool for an interactive chat session:
// the chat agent is caller node 0, so every delegation it makes becomes a
// root of the supervisor's tree. Events tee to .neo/events.jsonl and to the
// returned channel (the TUI's live checklist) for the session's lifetime.
func chatRunStepTool(prov llm.Provider, cfg *config.Config, cwd, root string, resolver factory.Resolver) (factory.RunStepTool, <-chan factory.Event) {
	runner := &factory.AgentRunner{
		Provider:     prov,
		DefaultModel: cfg.Model,
		Root:         root,
		BashTimeout:  5 * time.Minute,
		// A readonly session must not gain write access by delegating;
		// ask-mode sessions gate at the run_step approval instead (steps
		// run autonomously once approved). Mode is fixed at session start.
		Mode: permission.Mode(cfg.Permissions.Mode),
	}
	sup := factory.NewSupervisor(runner, factory.DefaultBudget(), resolver)
	runner.Sup = sup
	ui := make(chan factory.Event, 256)
	go teeEvents(sup.Events, filepath.Join(root, ".neo", "events.jsonl"), ui)
	return factory.RunStepTool{Sup: sup, CallerNode: 0, Dir: cwd}, ui
}

// stepsSection builds the system prompt block that advertises available
// steps, so plain language like "run the validate step" reliably maps to
// run_step calls.
func stepsSection(r factory.Resolver) string {
	cat := r.Catalog()
	if len(cat) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n# Workflow steps\n\n")
	b.WriteString("Named steps are runnable with the run_step tool. When the user asks to run ")
	b.WriteString("a step (\"run the validate step\") or a sequence (\"run these steps: branch, ")
	b.WriteString("verify, validate\"), call run_step once per step, in order, passing a ")
	b.WriteString("self-contained input each time — steps have no memory of this conversation. ")
	b.WriteString("If a step fails or its output reveals a problem, stop the sequence and report. ")
	b.WriteString("ok=true only means the step completed; judge an agent step's output by its content.\n\nAvailable steps:\n")
	for _, st := range cat {
		b.WriteString("\n- `")
		b.WriteString(st.Name)
		b.WriteString("`")
		if st.Description != "" {
			b.WriteString(" — ")
			b.WriteString(st.Description)
		}
		if st.Kind == "script" {
			b.WriteString(" (script)")
		}
	}
	return b.String()
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

func runSupervised(ctx context.Context, budget factory.Budget, rootStep, input string) (string, error) {
	cfg := mustConfig()
	prov := mustProvider(cfg)
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	root := workspace.Root(cwd)

	runner := &factory.AgentRunner{
		Provider:     prov,
		DefaultModel: cfg.Model,
		Root:         root,
		BashTimeout:  5 * time.Minute,
	}
	sup := factory.NewSupervisor(runner, budget, factory.Resolver{Paths: factory.DefaultStepPaths(root)})
	runner.Sup = sup

	console := factory.NewConsole(sup, os.Stdout)
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		console.Watch(filepath.Join(root, ".neo", "events.jsonl"), 500*time.Millisecond)
	}()

	out, err := sup.Run(ctx, cwd, rootStep, input)
	close(sup.Events)
	<-watcherDone
	fmt.Println()
	return out, err
}
