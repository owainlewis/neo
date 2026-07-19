package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/factory"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/projectctx"
	"github.com/owainlewis/neo/internal/session"
	"github.com/owainlewis/neo/internal/skills"
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
	if err := logx.InitFromEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: NEO_LOG: %v\n", err)
	}
	defer func() { _ = logx.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logx.Debug("neo start", "args", logx.SafeAny(os.Args[1:]))

	// `neo` with no subcommand defaults to chat — the common case.
	if len(os.Args) < 2 {
		runChat(ctx)
		return
	}

	switch os.Args[1] {
	case "chat":
		runChat(ctx)
	case "sessions":
		runSessions(ctx, os.Args[2:])
	case "doctor":
		os.Exit(runDoctor(ctx))
	case "resume":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: neo resume <session-id>")
			os.Exit(2)
		}
		resumeSession(ctx, os.Args[2])
	case "login":
		runLogin(ctx)
	case "logout":
		runLogout()
	case "-h", "--help", "help":
		printUsage()
	default:
		logx.Debug("neo unknown command", "command", os.Args[1])
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(2)
	}
}

const usageText = `neo — a Go coding agent

USAGE:
  neo                Interactive chat mode (default)
  neo chat           Interactive chat mode (explicit)
  neo sessions       List saved chat sessions
  neo sessions search <query>
                     Search saved session transcripts
  neo doctor         Check local config and environment
  neo resume <id>    Resume a saved chat session
  neo login          Log in to an OpenAI ChatGPT/Codex subscription (device code)
  neo logout         Remove stored subscription credentials
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
  then run "neo login".`

func printUsage() {
	fmt.Println(usageText)
}

func newRegistry(cwd, root string, extra ...tools.Tool) *tools.Registry {
	base := []tools.Tool{
		tools.Bash{Timeout: 2 * time.Minute, CWD: cwd},
		tools.ReadFile{},
		tools.WriteFile{},
		tools.EditFile{},
		tools.Grep{Root: root},
		tools.Glob{Root: root},
	}
	return tools.NewRegistry(append(base, extra...)...)
}

// chatSystem builds the chat agent's system prompt as ordered blocks: a stable,
// cacheable base (the static instructions plus the skill catalog) followed by
// uncached dynamic session context blocks. Splitting it this way lets prompt
// caching reuse the base across turns and sessions while the project tail
// varies. Discovery errors are non-fatal, warning and falling back to the blocks
// built so far rather than failing to start.
func chatSystem(cfg *config.Config, cwd string, sk []skills.Skill) (string, []llm.SystemBlock) {
	// Base block: static instructions + skill catalog. Stable within a session
	// and largely reused across them, so it's the cache breakpoint.
	base := skills.Augment(chatSystemPrompt, sk)
	cache := cfg.PromptCachingEnabled()
	blocks := []llm.SystemBlock{{Text: base, Cache: cache}}
	if cfg.AgentsFileEnabled() && cwd != "" {
		if docs, err := projectctx.Load(cwd); err != nil {
			fmt.Fprintf(os.Stderr, "warning: AGENTS.md: %v\n", err)
		} else if section := projectctx.Augment("", docs); section != "" {
			// Dynamic tail: kept uncached and after the breakpoint so it never
			// evicts the cached base.
			blocks = append(blocks, llm.SystemBlock{Text: section})
		}
	}
	var b strings.Builder
	for _, blk := range blocks {
		b.WriteString(blk.Text)
	}
	return b.String(), blocks
}

func mustConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	return cfg
}

func runChat(ctx context.Context) {
	store := mustSessionStore()
	runChatSession(ctx, store, nil)
}

func resumeSession(ctx context.Context, id string) {
	store := mustSessionStore()
	sess, err := store.Load(ctx, id)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "session not found: %s\n", id)
		} else {
			fmt.Fprintf(os.Stderr, "load session: %v\n", err)
		}
		os.Exit(1)
	}
	restoreSessionCWD(sess.Metadata.CWD)
	runChatSession(ctx, store, sess)
}

func restoreSessionCWD(cwd string) {
	if cwd == "" {
		return
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "warning: session cwd %s is unavailable; using current directory\n", cwd)
		return
	}
	if err := os.Chdir(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "warning: session cwd %s: %v; using current directory\n", cwd, err)
	}
}

func runChatSession(ctx context.Context, store *session.Store, sess *session.Session) {
	cfg := mustConfig()
	providerName, model := sessionBackend(cfg, sessionMetadata(sess))
	prov, err := chatSessionProvider(ctx, cfg, sess, providerName)
	if err != nil && providerName != cfg.Provider {
		fmt.Fprintf(os.Stderr, "warning: cannot resume with %s (%v); continuing with %s model %s\n", providerName, err, cfg.Provider, cfg.Model)
		providerName, model = cfg.Provider, cfg.Model
		prov = mustProvider(cfg)
	} else if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	cwd, _ := os.Getwd() // "" on failure → cwd-dependent capabilities are skipped
	root := workspace.Root(cwd)
	// The chat agent is the primary coordinator. It gets the agent tool (as
	// caller node 0) so it can spawn amnesiac subagents with self-contained
	// prompts directly from the conversation. Sequencing is the agent's
	// judgment, not a stored workflow artifact.
	var extra []tools.Tool
	var stepEvents <-chan factory.Event
	var agentRunner *factory.AgentRunner
	var agentRunnerFollowsCoordinator bool
	var workflowEvents <-chan workflow.Event
	wf := make(chan workflow.Event, 128)
	workflowEvents = wf
	extra = append(extra, workflow.Tool{Events: wf})
	if cwd != "" {
		var at factory.AgentTool
		workerProvider, workerModel, followsCoordinator := subagentBackend(ctx, cfg, prov, model)
		agentRunnerFollowsCoordinator = followsCoordinator
		at, stepEvents, agentRunner = chatAgentTool(workerProvider, workerModel, cfg, cwd, root)
		extra = append(extra, at)
	}
	reg := newRegistry(cwd, root, extra...)

	if sess == nil {
		var err error
		sess, err = store.Create(ctx, session.Metadata{
			CWD:      cwd,
			Model:    model,
			Provider: providerName,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "create session: %v\n", err)
			os.Exit(1)
		}
	}
	// Skills are loaded once: the catalog is advertised in the system prompt
	// (via chatSystem), and the same slice drives $name and /name expansion in
	// the TUI.
	sk := loadSkills(cfg, cwd)

	system, systemBlocks := chatSystem(cfg, cwd, sk)
	ag := agent.New(agent.Config{
		Model:        model,
		System:       system,
		SystemBlocks: systemBlocks,
		Provider:     prov,
		Tools:        reg,
		Policy:       permission.New(cfg.Permissions.Mode, root),
		Compactor:    chatCompactor(prov, model, cfg),
		Messages:     sess.Messages,
		Usage:        sess.Usage,
	})

	saveSession := func() error {
		activeProvider, activeModel := ag.Backend()
		sess.Messages = ag.Transcript()
		sess.Usage = ag.Usage()
		sess.Metadata.CWD = cwd
		sess.Metadata.Model = activeModel
		sess.Metadata.Provider = activeProvider
		return store.Save(ctx, sess)
	}

	switchModel := func(nextModel string) error {
		if agentRunner != nil && agentRunnerFollowsCoordinator {
			if err := agentRunner.SetBackend(prov, nextModel); err != nil {
				return err
			}
		}
		return ag.SetBackend(prov, nextModel, chatCompactor(prov, nextModel, cfg))
	}

	if err := tui.Run(ctx, ag, model, Version, sk,
		tui.WithAfterSend(saveSession),
		tui.WithModelSwitcher(providerName, modelChoices(ctx, cfg, providerName), switchModel),
		tui.WithStepEvents(stepEvents),
		tui.WithWorkflowEvents(workflowEvents),
		tui.WithVerbose(cfg.VerboseEnabled()),
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func chatCompactor(prov llm.Provider, model string, cfg *config.Config) compact.Compactor {
	s := compact.NewSummarizer(prov, model)
	if cfg != nil && cfg.Compaction.ContextWindowTokens > 0 {
		s.TriggerTokens = compact.TriggerTokensForContextWindow(cfg.Compaction.ContextWindowTokens)
	}
	return s
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
func loadSkills(cfg *config.Config, cwd string) []skills.Skill {
	if !cfg.SkillsEnabled() || cwd == "" {
		return nil
	}
	sk, err := skills.Load(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: skills: %v\n", err)
		return nil
	}
	return sk
}
