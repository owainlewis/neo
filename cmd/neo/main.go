package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/artifact"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/flow"
	"github.com/owainlewis/neo/internal/llm/anthropic"
	"github.com/owainlewis/neo/internal/phase"
	"github.com/owainlewis/neo/internal/tools"
	"github.com/owainlewis/neo/internal/tui"
	"github.com/owainlewis/neo/internal/ui"
)

const chatSystemPrompt = `You are neo, a focused coding agent.

Operate in the user's current working directory. Use the available tools to read files,
inspect code with bash, and make edits. Prefer small, verified changes. Run tests after
you change code. When you finish a task, briefly summarize what changed.`

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
	case "chat":
		runChat(ctx)
	case "flow":
		runFlow(ctx, os.Args[2:])
	case "phase":
		runPhase(ctx, os.Args[2:])
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Println(`neo — a Go coding agent with phased flows

USAGE:
  neo chat                          Interactive chat mode
  neo flow <flow-name> "<task>"     Run a named flow against a task
  neo phase <phase-name> "<task>"   Run a single phase
  neo help                          Show this help

CONFIG (env):
  ANTHROPIC_API_KEY    required
  NEO_MODEL            default: claude-sonnet-4-5
  NEO_PHASES_DIR       default: ./phases or ~/.neo/phases
  NEO_FLOWS_DIR        default: ./flows  or ~/.neo/flows`)
}

func newRegistry() *tools.Registry {
	return tools.NewRegistry(
		tools.Bash{Timeout: 2 * time.Minute},
		tools.ReadFile{},
		tools.WriteFile{},
		tools.EditFile{},
	)
}

func newProvider() (*anthropic.Client, error) {
	return anthropic.New()
}

func runChat(ctx context.Context) {
	cfg := config.Default()
	prov, err := newProvider()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	reg := newRegistry()

	ag := agent.New(agent.Config{
		Model:    cfg.Model,
		System:   chatSystemPrompt,
		Provider: prov,
		Tools:    reg,
	})

	if err := tui.Run(ctx, ag, cfg.Model); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runFlow(ctx context.Context, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: neo flow <flow-name> \"<task>\"")
		os.Exit(2)
	}
	flowName := args[0]
	task := strings.Join(args[1:], " ")

	cfg := config.Default()
	prov, err := newProvider()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	reg := newRegistry()
	store := artifact.NewStore(cfg.ArtifactsDir)

	printer := &ui.StatusPrinter{Out: os.Stdout}

	pr := &phase.Runner{
		Provider:     prov,
		Tools:        reg,
		DefaultModel: cfg.Model,
		OnEvent:      printer.Event,
	}

	flowPath := filepath.Join(cfg.FlowsDir, flowName+".yaml")
	def, err := flow.LoadDefinition(flowPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load flow %s: %v\n", flowPath, err)
		os.Exit(1)
	}

	runner := &flow.Runner{
		PhasesDir: cfg.PhasesDir,
		Runner:    pr,
		Store:     store,
		OnStatus:  printer.Status,
		OnEvent:   printer.Event,
	}

	if err := runner.Run(ctx, *def, task); err != nil {
		fmt.Fprintf(os.Stderr, "\nflow failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\nflow completed.")
}

func runPhase(ctx context.Context, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: neo phase <phase-name> \"<task>\"")
		os.Exit(2)
	}
	name := args[0]
	task := strings.Join(args[1:], " ")

	cfg := config.Default()
	prov, err := newProvider()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	reg := newRegistry()
	printer := &ui.StatusPrinter{Out: os.Stdout}

	pr := &phase.Runner{
		Provider:     prov,
		Tools:        reg,
		DefaultModel: cfg.Model,
		OnEvent:      printer.Event,
	}

	pdef := phase.Definition{Name: name, PromptPath: filepath.Join(cfg.PhasesDir, name+".md")}
	result, err := pr.Run(ctx, pdef, phase.Input{Task: task})
	if err != nil {
		fmt.Fprintf(os.Stderr, "phase failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result.Output)
}
