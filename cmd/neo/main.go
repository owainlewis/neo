package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm/anthropic"
	"github.com/owainlewis/neo/internal/projectctx"
	"github.com/owainlewis/neo/internal/tools"
	"github.com/owainlewis/neo/internal/tui"
)

// Version is overridable at build time via -ldflags "-X main.Version=...".
// Default "dev" makes local builds obvious in the splash screen.
var Version = "dev"

const chatSystemPrompt = `You are neo, a focused coding agent.

Operate in the user's current working directory. Use the available tools to read files,
inspect code with bash, and make edits. Prefer small, verified changes. Run tests after
you change code. When you finish a task, briefly summarize what changed.`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// `neo` with no subcommand defaults to chat — the common case.
	if len(os.Args) < 2 {
		runChat(ctx)
		return
	}

	switch os.Args[1] {
	case "chat":
		runChat(ctx)
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Println(`neo — a Go coding agent

USAGE:
  neo                Interactive chat mode (default)
  neo chat           Interactive chat mode (explicit)
  neo help           Show this help

CONFIG:
  Reads neo.yaml (cwd) → ~/.neo/config.yaml → embedded defaults.

  ANTHROPIC_API_KEY    required`)
}

func newRegistry() *tools.Registry {
	return tools.NewRegistry(
		tools.Bash{Timeout: 2 * time.Minute},
		tools.ReadFile{},
		tools.WriteFile{},
		tools.EditFile{},
	)
}

// chatSystem builds the chat agent's system prompt: the base instructions plus,
// when the feature is enabled, any AGENTS.md project context discovered from the
// working directory. A discovery error is non-fatal — it degrades to the base
// prompt with a warning rather than failing to start.
func chatSystem(cfg *config.Config) string {
	if !cfg.AgentsFileEnabled() {
		return chatSystemPrompt
	}
	cwd, err := os.Getwd()
	if err != nil {
		return chatSystemPrompt
	}
	docs, err := projectctx.Load(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: AGENTS.md: %v\n", err)
		return chatSystemPrompt
	}
	return projectctx.Augment(chatSystemPrompt, docs)
}

func mustConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	return cfg
}

func mustProvider() *anthropic.Client {
	prov, err := anthropic.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return prov
}

func runChat(ctx context.Context) {
	cfg := mustConfig()
	prov := mustProvider()
	reg := newRegistry()

	ag := agent.New(agent.Config{
		Model:    cfg.Model,
		System:   chatSystem(cfg),
		Provider: prov,
		Tools:    reg,
	})

	if err := tui.Run(ctx, ag, cfg.Model, Version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
