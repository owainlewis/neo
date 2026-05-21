package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/artifact"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm/anthropic"
	"github.com/owainlewis/neo/internal/phase"
	"github.com/owainlewis/neo/internal/tools"
	"github.com/owainlewis/neo/internal/tui"
	"github.com/owainlewis/neo/internal/workflow"
)

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
	case "flow":
		runFlow(ctx, os.Args[2:])
	case "step":
		runStep(ctx, os.Args[2:])
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Println(`neo — a Go coding agent with chat-owned flows

USAGE:
  neo                               Interactive chat mode (default)
  neo chat                          Interactive chat mode (explicit)
  neo flow <flow-name> "<task>"     Run a named flow against a task (headless)
  neo step <step-name> "<task>"     Run a single step (headless)
  neo help                          Show this help

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
		System:   chatSystemPrompt,
		Provider: prov,
		Tools:    reg,
	})

	wf := tui.WorkflowConfig{
		Config: cfg,
		Runner: &phase.Runner{
			Provider:     prov,
			Tools:        reg,
			DefaultModel: cfg.Model,
		},
		Store: artifact.NewStore(cfg.ArtifactsDir),
	}

	if err := tui.Run(ctx, ag, cfg.Model, wf); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runFlow is the headless developer-facing CLI surface for workflows.
// Identical engine to /run in chat; prints status as plain lines so output
// is grep-friendly in CI.
func runFlow(ctx context.Context, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, `usage: neo flow <flow-name> "<task>"`)
		os.Exit(2)
	}
	name := args[0]
	task := strings.Join(args[1:], " ")

	cfg := mustConfig()
	fc, ok := cfg.Flows[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "no flow %q in config (%s)\nAvailable: %s\n",
			name, cfg.Source(), strings.Join(cfg.FlowNames(), ", "))
		os.Exit(1)
	}

	prov := mustProvider()
	reg := newRegistry()
	pr := &phase.Runner{Provider: prov, Tools: reg, DefaultModel: cfg.Model}
	store := artifact.NewStore(cfg.ArtifactsDir)

	eng := &workflow.Engine{
		Resolver: cfg,
		Runner:   pr,
		Store:    store,
		Sink:     &cliSink{out: os.Stdout},
	}
	def := workflow.Definition{
		Name:      name,
		Steps:     fc.Steps,
		RetryFrom: fc.RetryFrom,
		MaxRounds: fc.MaxRounds,
	}
	if err := eng.Run(ctx, def, task); err != nil {
		fmt.Fprintf(os.Stderr, "\nflow failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\nflow completed.")
}

// runStep runs a single step against a task using the configured step
// resolution. Useful for iterating on a step's prompt without invoking a
// whole flow.
func runStep(ctx context.Context, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, `usage: neo step <step-name> "<task>"`)
		os.Exit(2)
	}
	name := args[0]
	task := strings.Join(args[1:], " ")

	cfg := mustConfig()
	def, err := cfg.ResolveStep(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	prov := mustProvider()
	reg := newRegistry()
	pr := &phase.Runner{Provider: prov, Tools: reg, DefaultModel: cfg.Model}

	result, err := pr.Run(ctx, def, phase.Input{Task: task})
	if err != nil {
		fmt.Fprintf(os.Stderr, "step failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result.Output)
}

// cliSink prints workflow events as plain lines on stdout. Minimal, no
// spinner — suitable for piping to a log or CI output.
type cliSink struct {
	out *os.File
}

func (s *cliSink) OnWorkflow(e workflow.Event) {
	switch e.Kind {
	case workflow.StepStarted:
		fmt.Fprintf(s.out, "▸ %s (%d/%d) round %d\n", e.Step, e.Index, e.Total, e.Round)
	case workflow.StepCompleted:
		fmt.Fprintf(s.out, "✓ %s\n", e.Step)
	case workflow.StepFailed:
		fmt.Fprintf(s.out, "✗ %s — %s\n", e.Step, e.Message)
	case workflow.RoundRetrying:
		fmt.Fprintf(s.out, "↻ retrying from %s (round %d)\n", e.Step, e.Round)
	case workflow.WorkflowCompleted:
		fmt.Fprintln(s.out, "✓ workflow completed")
	case workflow.WorkflowFailed:
		fmt.Fprintf(s.out, "✗ workflow failed — %s\n", e.Message)
	}
}

func (s *cliSink) OnAgent(_ string, _ agent.Event) {
	// Headless mode: don't print fine-grained agent events to stdout.
}
