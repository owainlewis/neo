package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/approval"
	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/phase"
	"github.com/owainlewis/neo/internal/projectctx"
	"github.com/owainlewis/neo/internal/session"
	"github.com/owainlewis/neo/internal/skills"
	"github.com/owainlewis/neo/internal/subagent"
	"github.com/owainlewis/neo/internal/tools"
	"github.com/owainlewis/neo/internal/tui"
	"github.com/owainlewis/neo/internal/workflow"
	"github.com/owainlewis/neo/internal/workspace"
)

// Version is overridable at build time via -ldflags "-X main.Version=...".
// Default "dev" makes local builds obvious in the splash screen.
var Version = "dev"

const chatSystemPrompt = `You are neo, a focused coding agent.

Operate in the user's current working directory. Use the available tools to read files,
inspect code with bash, and make edits. Prefer small, verified changes. Run tests after
you change code. When you finish a task, briefly summarize what changed.

Before tool calls, write one short sentence explaining what you are checking or
changing and why. Do not narrate obvious individual calls or expose private reasoning.
Issue independent reads, searches, or inspections together in one response.

For multi-step tasks, or when workflow instructions are provided, create a
visible workflow checklist with the workflow tool before doing the work.
Workflow instructions may come from the user's request,
AGENTS.md, an invoked skill, or your own plan; always render them through the
workflow tool. Preserve the wording and order of user-provided numbered steps.
Do not invent a workflow for a simple single-step request. Mark each high-level
item running before working on it, and mark it done, failed, or skipped based
on the outcome.
When the user asks for a coordinator-worker or orchestrated-agent flow, treat the
chat agent as the coordinator: plan first, delegate suitable self-contained tasks
to subagents with the agent tool, inspect their results, and keep workflow
statuses based on evidence. Do not mirror every tool call manually; Neo attaches
tool and subagent activity to the active workflow item automatically. Write
subagent prompts dynamically from the user's goal and current context; use the
normal tools directly when delegation is unnecessary.`

func main() {
	os.Exit(execute(os.Args[1:], stdio{
		in:  os.Stdin,
		out: os.Stdout,
		err: os.Stderr,
	}, lifecycle{
		init:  logx.InitFromEnv,
		close: logx.Close,
	}))
}

type stdio struct {
	in  io.Reader
	out io.Writer
	err io.Writer
}

type lifecycle struct {
	init  func() error
	close func() error
}

// execute owns process-lifetime setup and cleanup. It returns before main
// terminates the process, so deferred cleanup is never skipped on failures.
func execute(args []string, streams stdio, life lifecycle) int {
	if err := life.init(); err != nil {
		fmt.Fprintf(streams.err, "warning: NEO_LOG: %v\n", err)
	}
	defer func() { _ = life.close() }()
	return run(args, streams)
}

// run is the testable command boundary. It owns signal handling, dispatches one
// command, and returns the process exit code without terminating the process.
func run(args []string, streams stdio) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logx.Debug("neo start", "args", logx.SafeAny(args))

	// `neo` with no subcommand defaults to chat — the common case.
	if len(args) == 0 {
		return runChat(ctx, streams)
	}

	switch args[0] {
	case "chat":
		return runChat(ctx, streams)
	case "run":
		return runHeadless(ctx, args[1:], streams)
	case "sessions":
		return runSessions(ctx, args[1:], streams)
	case "doctor":
		return runDoctor(ctx, streams.out)
	case "resume":
		if len(args) < 2 {
			fmt.Fprintln(streams.err, "usage: neo resume <session-id>")
			return 2
		}
		return resumeSession(ctx, args[1], streams)
	case "login":
		return runLogin(ctx, streams)
	case "logout":
		return runLogout(streams)
	case "-v", "--version", "version":
		printVersion(streams.out)
		return 0
	case "-h", "--help", "help":
		printUsage(streams.out)
		return 0
	default:
		logx.Debug("neo unknown command", "command", args[0])
		fmt.Fprintf(streams.err, "unknown command: %s\n", args[0])
		printUsage(streams.out)
		return 2
	}
}

const usageText = `neo — a Go coding agent

USAGE:
  neo                Interactive chat mode (default)
  neo chat           Interactive chat mode (explicit)
  neo run [options] <prompt>
                     Run one headless prompt and exit
  neo sessions       List saved chat sessions
  neo sessions search <query>
                     Search saved session transcripts
  neo doctor         Check local config and environment
  neo resume <id>    Resume a saved chat session
  neo login          Log in to an OpenAI ChatGPT/Codex subscription (device code)
  neo logout         Remove stored subscription credentials
  neo version        Show the Neo version (also -v, --version)
  neo help           Show this help

CONFIG:
  Reads neo.yaml (cwd) → ~/.neo/config.yaml → embedded defaults.
  Select a backend with the "provider" key: "anthropic" (default), "openai", "openrouter", or "google".

  ANTHROPIC_API_KEY    required when provider is "anthropic"
  OPENAI_API_KEY       required when provider is "openai" with api_key auth
  OPENROUTER_API_KEY   required when provider is "openrouter"
  GOOGLE_API_KEY       required when provider is "google"

  To use a ChatGPT subscription instead of an API key, set in neo.yaml:
    provider: openai
    openai_auth: subscription
  then run "neo login".

HEADLESS RUN:
  neo run --json --timeout 10m "Review this repo without changing files"
  cat prompt.md | neo run --json

  Options: --timeout <duration>, --json`

func printUsage(out io.Writer) {
	fmt.Fprintln(out, usageText)
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "neo version %s\n", Version)
}

func newRegistry(cwd, root string, extra ...tools.Tool) *tools.Registry {
	base := append([]tools.Tool{
		tools.Bash{Timeout: 2 * time.Minute, CWD: cwd},
		tools.Grep{Root: root},
		tools.Glob{Root: root},
	}, tools.NewFileTools()...)
	return tools.NewRegistry(append(base, extra...)...)
}

// chatSystem builds the chat agent's system prompt as ordered blocks: a stable,
// cacheable base (the static instructions plus phase and skill catalogs) followed by
// uncached dynamic session context blocks. Splitting it this way lets prompt
// caching reuse the base across turns and sessions while the project tail
// varies. Discovery errors are non-fatal, warning and falling back to the blocks
// built so far rather than failing to start.
func chatSystem(cfg *config.Config, cwd string, sk []skills.Skill, errOut io.Writer) (string, []llm.SystemBlock) {
	// Base block: static instructions plus phase and skill catalogs. Stable within a session
	// and largely reused across them, so it's the cache breakpoint.
	base := phase.Augment(chatSystemPrompt, cfg.NamedPhases())
	base = skills.Augment(base, sk)
	cache := cfg.PromptCachingEnabled()
	blocks := []llm.SystemBlock{{Text: base, Cache: cache}}
	// Dynamic tail: everything below is kept uncached and after the breakpoint
	// so it never evicts the cached base.
	if section := environmentSection(cwd, time.Now()); section != "" {
		blocks = append(blocks, llm.SystemBlock{Text: section})
	}
	if cfg.AgentsFileEnabled() && cwd != "" {
		docs, err := projectctx.Load(cwd)
		if err != nil {
			fmt.Fprintf(errOut, "warning: AGENTS.md: %v\n", err)
		}
		if section := projectctx.Augment("", docs); section != "" {
			blocks = append(blocks, llm.SystemBlock{Text: section})
		}
	}
	var b strings.Builder
	for _, blk := range blocks {
		b.WriteString(blk.Text)
	}
	return b.String(), blocks
}

// environmentSection states where the agent is running. Without it the model
// has to spend a bash call on pwd and has no idea what today's date is. These
// facts are fixed for the session, so a branch name is deliberately absent: it
// changes underneath us and the model can ask git when it matters.
func environmentSection(cwd string, now time.Time) string {
	if cwd == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n# Environment\n\n")
	fmt.Fprintf(&b, "Working directory: %s\n", cwd)
	if root := workspace.Root(cwd); root != cwd {
		fmt.Fprintf(&b, "Repository root: %s\n", root)
	}
	fmt.Fprintf(&b, "Platform: %s\n", runtime.GOOS)
	fmt.Fprintf(&b, "Today's date: %s\n", now.Format("2006-01-02"))
	return b.String()
}

func loadConfig(errOut io.Writer) (*config.Config, bool) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(errOut, "config: %v\n", err)
		return nil, false
	}
	return cfg, true
}

func runChat(ctx context.Context, streams stdio) int {
	store, ok := loadSessionStore(streams.err)
	if !ok {
		return 1
	}
	return runChatSession(ctx, store, nil, streams)
}

type headlessOptions struct {
	timeout time.Duration
	json    bool
}

type headlessResult struct {
	OK         bool   `json:"ok"`
	ElapsedMS  int64  `json:"elapsed_ms"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	ToolCalls  int    `json:"tool_calls"`
	ToolErrors int    `json:"tool_errors"`
	Final      string `json:"final,omitempty"`
	Error      string `json:"error,omitempty"`
}

func runHeadless(ctx context.Context, args []string, streams stdio) int {
	opts, prompt, err := parseHeadlessArgs(args, streams.in)
	if err != nil {
		fmt.Fprintln(streams.err, err)
		fmt.Fprintln(streams.err, "usage: neo run [--json] [--timeout 10m] <prompt>")
		return 2
	}
	if opts.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.timeout)
		defer cancel()
	}
	started := time.Now()
	cfg, ok := loadConfig(streams.err)
	if !ok {
		return 1
	}
	providerName, model := cfg.Provider, cfg.Model
	prov, err := checkedProvider(ctx, cfg, providerName)
	if err != nil {
		finishHeadless(opts, headlessResult{OK: false, ElapsedMS: time.Since(started).Milliseconds(), Provider: providerName, Model: model, Error: err.Error()}, streams)
		return 1
	}
	cwd, _ := os.Getwd()
	root := workspace.Root(cwd)
	sk := loadSkills(cfg, cwd, streams.err)
	system, systemBlocks := chatSystem(cfg, cwd, sk, streams.err)

	var toolCalls, toolErrors int
	reg := newRegistry(cwd, root)
	ag := agent.New(agent.Config{
		Model:        model,
		System:       system,
		SystemBlocks: systemBlocks,
		Provider:     prov,
		Tools:        reg,
		Compactor:    chatCompactor(prov, model, cfg),
		OnEvent: func(e agent.Event) {
			switch e.Kind {
			case agent.EventToolCall:
				toolCalls++
			case agent.EventToolResult:
				if e.IsError {
					toolErrors++
				}
			}
		},
	})
	out, err := ag.Send(ctx, prompt)
	result := headlessResult{
		OK:         err == nil,
		ElapsedMS:  time.Since(started).Milliseconds(),
		Provider:   prov.Name(),
		Model:      model,
		ToolCalls:  toolCalls,
		ToolErrors: toolErrors,
		Final:      out,
	}
	if err != nil {
		result.Error = err.Error()
	}
	finishHeadless(opts, result, streams)
	if err != nil {
		return 1
	}
	return 0
}

func parseHeadlessArgs(args []string, stdin io.Reader) (headlessOptions, string, error) {
	opts := headlessOptions{timeout: 10 * time.Minute}
	for _, arg := range args {
		if arg == "--permission" || arg == "-permission" ||
			strings.HasPrefix(arg, "--permission=") || strings.HasPrefix(arg, "-permission=") {
			return opts, "", fmt.Errorf("--permission has been removed; run Neo inside a sandbox and use tool_approvals for optional interactive confirmations")
		}
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "maximum wall-clock duration")
	fs.BoolVar(&opts.json, "json", false, "print a JSON summary instead of plain text")
	if err := fs.Parse(args); err != nil {
		return opts, "", err
	}
	parts := fs.Args()
	if stdin != nil && !isCharacterDevice(stdin) {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return opts, "", fmt.Errorf("read stdin: %w", err)
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			parts = append([]string{s}, parts...)
		}
	}
	prompt := strings.TrimSpace(strings.Join(parts, " "))
	if prompt == "" {
		return opts, "", fmt.Errorf("neo run: missing prompt")
	}
	return opts, prompt, nil
}

func isCharacterDevice(in io.Reader) bool {
	input, ok := in.(interface {
		Stat() (os.FileInfo, error)
	})
	if !ok {
		return false
	}
	info, err := input.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func finishHeadless(opts headlessOptions, result headlessResult, streams stdio) {
	if opts.json {
		b, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fmt.Fprintf(streams.err, "encode result: %v\n", err)
			return
		}
		fmt.Fprintln(streams.out, string(b))
		return
	}
	if result.Final != "" {
		fmt.Fprintln(streams.out, result.Final)
	}
	if result.Error != "" {
		fmt.Fprintln(streams.err, result.Error)
	}
}

func resumeSession(ctx context.Context, id string, streams stdio) int {
	store, ok := loadSessionStore(streams.err)
	if !ok {
		return 1
	}
	sess, err := store.Load(ctx, id)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			fmt.Fprintf(streams.err, "session not found: %s\n", id)
		} else {
			fmt.Fprintf(streams.err, "load session: %v\n", err)
		}
		return 1
	}
	restoreSessionCWD(sess.Metadata.CWD, streams.err)
	return runChatSession(ctx, store, sess, streams)
}

func restoreSessionCWD(cwd string, errOut io.Writer) {
	if cwd == "" {
		return
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(errOut, "warning: session cwd %s is unavailable; using current directory\n", cwd)
		return
	}
	if err := os.Chdir(cwd); err != nil {
		fmt.Fprintf(errOut, "warning: session cwd %s: %v; using current directory\n", cwd, err)
	}
}

func runChatSession(ctx context.Context, store *session.Store, sess *session.Session, streams stdio) int {
	cfg, ok := loadConfig(streams.err)
	if !ok {
		return 1
	}
	providerName, model := sessionBackend(cfg, sessionMetadata(sess), streams.err)
	prov, err := chatSessionProvider(ctx, cfg, sess, providerName)
	if err != nil && providerName != cfg.Provider {
		fmt.Fprintf(streams.err, "warning: cannot resume with %s (%v); continuing with %s model %s\n", providerName, err, cfg.Provider, cfg.Model)
		providerName, model = cfg.Provider, cfg.Model
		prov, err = newProvider(cfg, cfg.Provider)
		if err != nil {
			fmt.Fprintln(streams.err, err)
			return 1
		}
	} else if err != nil {
		fmt.Fprintln(streams.err, err)
		return 1
	}

	cwd, _ := os.Getwd() // "" on failure → cwd-dependent capabilities are skipped
	root := workspace.Root(cwd)
	// The chat agent is the primary coordinator. It gets the agent tool (as
	// caller node 0) so it can spawn amnesiac subagents with self-contained
	// prompts directly from the conversation. Sequencing is the agent's
	// judgment, not a stored workflow artifact.
	var extra []tools.Tool
	var stepEvents <-chan subagent.Event
	var agentRunner *subagent.AgentRunner
	var agentRunnerFollowsCoordinator bool
	var workflowEvents <-chan workflow.Event
	wf := make(chan workflow.Event, 128)
	workflowEvents = wf
	extra = append(extra, workflow.Tool{Events: wf})
	if cwd != "" {
		var at subagent.AgentTool
		workerProvider, workerModel, followsCoordinator := subagentBackend(ctx, cfg, prov, model)
		agentRunnerFollowsCoordinator = followsCoordinator
		at, stepEvents, agentRunner = chatAgentTool(workerProvider, workerModel, cwd, root, cfg)
		extra = append(extra, at)
	}
	reg := newRegistry(cwd, root, extra...)

	if sess == nil {
		var err error
		adapterName := prov.Name()
		sess, err = store.Create(ctx, session.Metadata{
			CWD:        cwd,
			Model:      model,
			Provider:   sessionProviderID(adapterName),
			OpenAIAuth: adapterOpenAIAuth(adapterName),
		})
		if err != nil {
			fmt.Fprintf(streams.err, "create session: %v\n", err)
			return 1
		}
	}
	// Skills are loaded once: the catalog is advertised in the system prompt
	// (via chatSystem), and the same slice drives $name and /name expansion in
	// the TUI.
	sk := loadSkills(cfg, cwd, streams.err)

	system, systemBlocks := chatSystem(cfg, cwd, sk, streams.err)
	var requiresApproval func(string, map[string]any) bool
	if len(cfg.ToolApprovals) > 0 {
		requiresApproval = approval.New(cfg.ToolApprovals).Requires
	}
	ag := agent.New(agent.Config{
		Model:            model,
		System:           system,
		SystemBlocks:     systemBlocks,
		Provider:         prov,
		Tools:            reg,
		Compactor:        chatCompactor(prov, model, cfg),
		RequiresApproval: requiresApproval,
		Messages:         sess.Messages,
		Usage:            sess.Usage,
	})

	saveSession := func() error {
		return saveChatSession(ctx, store, sess, ag, cwd)
	}

	switchModel := func(nextModel string) error {
		if agentRunner != nil && agentRunnerFollowsCoordinator {
			if err := agentRunner.SetBackend(prov, nextModel); err != nil {
				return err
			}
		}
		return ag.SetBackend(prov, nextModel, chatCompactor(prov, nextModel, cfg))
	}

	if err := tui.Run(ctx, ag, model, Version, cwd, sk,
		tui.WithAfterSend(saveSession),
		tui.WithPhases(cfg.NamedPhases()),
		tui.WithModelSwitcher(providerName, modelChoices(ctx, cfg, providerName, streams.err), switchModel),
		tui.WithStepEvents(stepEvents),
		tui.WithWorkflowEvents(workflowEvents),
		tui.WithVerbose(cfg.VerboseEnabled()),
		tui.WithIO(streams.in, streams.out),
	); err != nil {
		fmt.Fprintln(streams.err, err)
		return 1
	}
	return 0
}

func saveChatSession(ctx context.Context, store *session.Store, sess *session.Session, ag *agent.Agent, cwd string) error {
	activeProvider, activeModel := ag.Backend()
	sess.Messages = ag.Transcript()
	sess.Usage = ag.Usage()
	sess.Metadata.CWD = cwd
	sess.Metadata.Model = activeModel
	sess.Metadata.Provider = sessionProviderID(activeProvider)
	sess.Metadata.OpenAIAuth = adapterOpenAIAuth(activeProvider)
	return store.Save(ctx, sess)
}

func chatCompactor(prov llm.Provider, model string, cfg *config.Config) compact.Compactor {
	contextWindowTokens := 0
	if cfg != nil {
		contextWindowTokens = cfg.Compaction.ContextWindowTokens
	}
	return compact.NewSummarizerForContextWindow(prov, model, contextWindowTokens)
}

func sessionMetadata(sess *session.Session) session.Metadata {
	if sess == nil {
		return session.Metadata{}
	}
	return sess.Metadata
}

// sessionBackend restores a saved backend when its local credential source is
// still configured. Otherwise resume is explicit about falling back to the
// current config rather than applying a model id to the wrong provider.
// loadSkills discovers skills when the feature is enabled. A discovery error is
// non-fatal — it warns and returns no skills rather than failing to start.
func loadSkills(cfg *config.Config, cwd string, errOut io.Writer) []skills.Skill {
	if !cfg.SkillsEnabled() || cwd == "" {
		return nil
	}
	sk, err := skills.Load(cwd)
	if err != nil {
		fmt.Fprintf(errOut, "warning: skills: %v\n", err)
		return nil
	}
	return sk
}
