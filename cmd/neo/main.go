package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/auth"
	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/factory"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/anthropic"
	"github.com/owainlewis/neo/internal/llm/google"
	"github.com/owainlewis/neo/internal/llm/openai"
	"github.com/owainlewis/neo/internal/llm/openrouter"
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
	case "update":
		runUpdate(ctx, os.Args[2:])
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
  neo update         Install the latest stable release
  neo update --check Check for a stable release without installing
  neo update --nightly
                     Install the latest nightly release
  neo update --nightly --check
                     Check for a nightly release without installing
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
	root := ""
	if cwd != "" {
		root = workspace.Root(cwd)
	}

	if cfg.AgentsFileEnabled() && cwd != "" {
		if docs, err := projectctx.Load(cwd); err != nil {
			fmt.Fprintf(os.Stderr, "warning: AGENTS.md: %v\n", err)
		} else if section := projectctx.Augment("", docs); section != "" {
			// Dynamic tail: kept uncached and after the breakpoint so it never
			// evicts the cached base.
			blocks = append(blocks, llm.SystemBlock{Text: section})
		}
	}
	if cfg.MemoryEnabled() && root != "" {
		if doc, ok, err := projectctx.LoadMemory(root); err != nil {
			fmt.Fprintf(os.Stderr, "warning: memory.md: %v\n", err)
		} else if section := projectctx.MemorySection(doc); ok && section != "" {
			blocks = append(blocks, llm.SystemBlock{Text: section})
		}
	}
	if doc, ok := projectctx.LoadGitContext(cwd); ok {
		if section := projectctx.GitSection(doc); section != "" {
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

func mustProvider(cfg *config.Config) llm.Provider {
	prov, err := newProvider(cfg, cfg.Provider)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return prov
}

func newProvider(cfg *config.Config, name string) (llm.Provider, error) {
	switch name {
	case "openai":
		if cfg.OpenAIAuth == config.OpenAIAuthSubscription {
			return newCodexProvider()
		}
		return openai.New()
	case "openrouter":
		return openrouter.New()
	case "google":
		return google.New()
	case "anthropic", "":
		return anthropic.New()
	default:
		return nil, fmt.Errorf("unknown provider %q (expected \"anthropic\", \"openai\", \"openrouter\", or \"google\")", name)
	}
}

func checkedProvider(ctx context.Context, cfg *config.Config, name string) (llm.Provider, error) {
	if name == "openai" && cfg.OpenAIAuth == config.OpenAIAuthSubscription {
		store, err := auth.DefaultStore()
		if err != nil {
			return nil, err
		}
		if _, err := auth.NewTokenSource(store, auth.ProviderOpenAICodex).Token(ctx); err != nil {
			return nil, fmt.Errorf("OpenAI subscription credentials: %w", err)
		}
	}
	return newProvider(cfg, name)
}

func chatSessionProvider(ctx context.Context, cfg *config.Config, sess *session.Session, name string) (llm.Provider, error) {
	if sess != nil {
		return checkedProvider(ctx, cfg, name)
	}
	return newProvider(cfg, name)
}

// newCodexProvider builds the ChatGPT/Codex subscription client from stored
// device-code credentials, erroring clearly if the user hasn't logged in.
func newCodexProvider() (llm.Provider, error) {
	store, err := auth.DefaultStore()
	if err != nil {
		return nil, err
	}
	if _, ok, err := store.Get(auth.ProviderOpenAICodex); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("not logged in to an OpenAI subscription: run `neo login`")
	}
	src := auth.NewTokenSource(store, auth.ProviderOpenAICodex)
	return openai.NewCodex(codexCredentials{ts: src}), nil
}

// codexCredentials adapts auth.TokenSource to openai.CredentialSource.
type codexCredentials struct{ ts *auth.TokenSource }

func (c codexCredentials) Token(ctx context.Context) (accessToken, accountID string, err error) {
	cr, err := c.ts.Token(ctx)
	if err != nil {
		return "", "", err
	}
	return cr.AccessToken, cr.AccountID, nil
}

// runLogin performs the OpenAI subscription device-code flow and stores the
// result.
func runLogin(ctx context.Context) {
	store, err := auth.DefaultStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "login: %v\n", err)
		os.Exit(1)
	}

	creds, err := auth.LoginOpenAI(ctx, auth.LoginOptions{
		OnDeviceCode: func(url, code string) {
			fmt.Println("Log in to OpenAI with this device code:")
			fmt.Println("\n  " + url)
			fmt.Println("  Code: " + code + "\n")
			fmt.Println("The code expires after 15 minutes. Never share it.")
			fmt.Println("Waiting for authorization to complete...")
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "login failed: %v\n", err)
		os.Exit(1)
	}
	if err := store.Set(auth.ProviderOpenAICodex, creds); err != nil {
		fmt.Fprintf(os.Stderr, "save credentials: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Login complete. Credentials saved to " + store.Path() + ".")
	fmt.Println("Set `provider: openai` and `openai_auth: subscription` in neo.yaml to use them.")
}

// runLogout removes stored subscription credentials.
func runLogout() {
	store, err := auth.DefaultStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "logout: %v\n", err)
		os.Exit(1)
	}
	if err := store.Delete(auth.ProviderOpenAICodex); err != nil {
		fmt.Fprintf(os.Stderr, "logout: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Logged out of OpenAI subscription.")
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
	var workflowEvents <-chan workflow.Event
	wf := make(chan workflow.Event, 128)
	workflowEvents = wf
	extra = append(extra, workflow.Tool{Events: wf})
	if cwd != "" {
		var at factory.AgentTool
		at, stepEvents, agentRunner = chatAgentTool(prov, model, cfg, cwd, root)
		extra = append(extra, at)
	}
	reg := newRegistry(cwd, root, extra...)

	if sess == nil {
		var err error
		sess, err = store.Create(ctx, session.Metadata{
			Source:   session.DefaultSource,
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

	switchBackend := func(choice tui.ModelChoice) error {
		next, err := checkedProvider(ctx, cfg, choice.Provider)
		if err != nil {
			return fmt.Errorf("switch to %s: %w", choice.Provider, err)
		}
		if agentRunner != nil {
			if err := agentRunner.SetBackend(next, choice.ID); err != nil {
				return err
			}
		}
		return ag.SetBackend(next, choice.ID, chatCompactor(next, choice.ID, cfg))
	}

	if err := tui.Run(ctx, ag, model, Version, sk,
		tui.WithAfterSend(saveSession),
		tui.WithPermissionMode(cfg.Permissions.Mode),
		tui.WithProjectMemory(root, cfg.MemoryEnabled()),
		tui.WithSessions(store, sess, func(resumed *session.Session) error {
			resumedProvider, resumedModel := sessionBackend(cfg, resumed.Metadata)
			activeProvider, activeModel := ag.Backend()
			if resumedProvider != activeProvider || resumedModel != activeModel {
				if err := switchBackend(tui.ModelChoice{Provider: resumedProvider, ID: resumedModel}); err != nil {
					return err
				}
			}
			sess = resumed
			return nil
		}),
		tui.WithModelSwitcher(providerName, modelChoices(ctx, cfg, providerName), switchBackend),
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
func sessionBackend(cfg *config.Config, meta session.Metadata) (string, string) {
	if meta.Provider == "" || meta.Model == "" {
		return cfg.Provider, cfg.Model
	}
	if meta.Provider == cfg.Provider || providerCredentialPresent(cfg, meta.Provider) {
		return meta.Provider, meta.Model
	}
	fmt.Fprintf(os.Stderr, "warning: session provider %s is not configured; continuing with %s model %s\n",
		meta.Provider, cfg.Provider, cfg.Model)
	return cfg.Provider, cfg.Model
}

func modelChoices(ctx context.Context, cfg *config.Config, activeProvider string) []tui.ModelChoice {
	var choices []tui.ModelChoice
	for _, provider := range configuredProviders(cfg, activeProvider) {
		providerChoices := providerModelChoices(ctx, cfg, provider, provider == activeProvider)
		for i := range providerChoices {
			providerChoices[i].Provider = provider
		}
		choices = append(choices, providerChoices...)
	}
	return choices
}

// configuredProviders treats an available local credential source as provider
// configuration. The active provider is always first because chat startup has
// already validated it; additional providers require their credential to be
// present before they enter the picker.
func configuredProviders(cfg *config.Config, active string) []string {
	if active == "" {
		active = cfg.Provider
		if active == "" {
			active = "anthropic"
		}
	}
	ordered := []string{active, "anthropic", "openai", "openrouter", "google"}
	seen := map[string]bool{}
	var providers []string
	for _, provider := range ordered {
		if seen[provider] || (provider != active && !providerCredentialPresent(cfg, provider)) {
			continue
		}
		seen[provider] = true
		providers = append(providers, provider)
	}
	return providers
}

func providerCredentialPresent(cfg *config.Config, provider string) bool {
	switch provider {
	case "anthropic":
		return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != ""
	case "openai":
		if cfg.OpenAIAuth == config.OpenAIAuthSubscription {
			store, err := auth.DefaultStore()
			if err != nil {
				return false
			}
			_, ok, err := store.Get(auth.ProviderOpenAICodex)
			return err == nil && ok
		}
		return strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != ""
	case "openrouter":
		return strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) != ""
	case "google":
		return strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) != ""
	default:
		return false
	}
}

func providerModelChoices(ctx context.Context, cfg *config.Config, provider string, active bool) []tui.ModelChoice {
	switch provider {
	case "openai":
		if cfg.OpenAIAuth == config.OpenAIAuthSubscription {
			return []tui.ModelChoice{
				{ID: "gpt-5-codex", Name: "GPT-5 Codex", Description: "Supported ChatGPT/Codex subscription model"},
			}
		}
		return []tui.ModelChoice{
			{ID: "gpt-5.2", Name: "GPT-5.2", Description: "Recommended flagship model for coding and agentic tasks"},
			{ID: "gpt-5.1", Name: "GPT-5.1", Description: "Coding and agentic model with configurable reasoning"},
			{ID: "gpt-5", Name: "GPT-5", Description: "Previous GPT-5 reasoning model"},
			{ID: "gpt-5-mini", Name: "GPT-5 mini", Description: "Faster, lower-cost GPT-5 model"},
			{ID: "gpt-5-nano", Name: "GPT-5 nano", Description: "Smallest GPT-5 model"},
			{ID: "gpt-4.1", Name: "GPT-4.1", Description: "Non-reasoning model for general coding tasks"},
			{ID: "gpt-4o", Name: "GPT-4o", Description: "Fast multimodal GPT-4o model"},
			{ID: "gpt-4o-mini", Name: "GPT-4o mini", Description: "Smaller GPT-4o model"},
		}
	case "openrouter":
		if !active {
			return []tui.ModelChoice{
				{ID: openrouter.DefaultModel, Name: openrouter.DefaultModel, Description: "Default OpenRouter model"},
			}
		}
		return openRouterModelChoices(ctx)
	case "google":
		return []tui.ModelChoice{
			{ID: google.DefaultModel, Name: "Gemini 3.5 Flash", Description: "Stable Google Gemini model for coding and agentic tasks"},
			{ID: "gemini-3.1-pro-preview", Name: "Gemini 3.1 Pro Preview", Description: "Higher-capability preview model for complex coding tasks"},
			{ID: "gemini-3.1-flash-lite", Name: "Gemini 3.1 Flash-Lite", Description: "Lower-cost stable Gemini model"},
		}
	default:
		return []tui.ModelChoice{
			{ID: "claude-opus-4-8", Name: "Claude Opus 4.8", Description: "Default Anthropic model"},
		}
	}
}

// openRouterModelChoices fetches the live OpenRouter model catalogue. Model ids
// move fast, so the picker is populated from OpenRouter's /models endpoint rather
// than a hardcoded list. On failure (offline, timeout, API change) it falls back
// to the provider default so the picker still works. The fetch is time-boxed so
// startup never hangs on a slow network.
func openRouterModelChoices(ctx context.Context) []tui.ModelChoice {
	fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	models, err := openrouter.Models(fetchCtx, nil)
	if err != nil || len(models) == 0 {
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not fetch OpenRouter models (%v); using default\n", err)
		}
		return []tui.ModelChoice{
			{ID: openrouter.DefaultModel, Name: openrouter.DefaultModel, Description: "Default OpenRouter model"},
		}
	}

	choices := make([]tui.ModelChoice, 0, len(models))
	for _, m := range models {
		name := m.Name
		if name == "" {
			name = m.ID
		}
		choices = append(choices, tui.ModelChoice{ID: m.ID, Name: name, Description: m.Description})
	}
	return choices
}

func runSessions(ctx context.Context, args []string) {
	if len(args) == 0 {
		listSessions(ctx)
		return
	}
	if args[0] == "search" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: neo sessions search <query>")
			os.Exit(2)
		}
		searchSessions(ctx, strings.Join(args[1:], " "))
		return
	}
	fmt.Fprintf(os.Stderr, "unknown sessions command: %s\n", args[0])
	fmt.Fprintln(os.Stderr, "usage: neo sessions [search <query>]")
	os.Exit(2)
}

func listSessions(ctx context.Context) {
	store := mustSessionStore()
	items, err := store.List(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list sessions: %v\n", err)
		os.Exit(1)
	}
	if len(items) == 0 {
		fmt.Println("no saved sessions")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tUPDATED\tMODEL\tCWD\tTITLE")
	for _, meta := range items {
		title := meta.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			meta.ID,
			meta.UpdatedAt.Local().Format("2006-01-02 15:04"),
			meta.Model,
			shortPath(meta.CWD),
			title,
		)
	}
	_ = w.Flush()
}

func searchSessions(ctx context.Context, query string) {
	store := mustSessionStore()
	results, warnings, err := store.Search(ctx, query)
	for _, warning := range warnings {
		fmt.Fprintf(os.Stderr, "warning: skipped session %s: %v\n", warning.ID, warning.Err)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "search sessions: %v\n", err)
		os.Exit(1)
	}
	if len(results) == 0 {
		fmt.Println("no matching sessions")
		return
	}
	printSessionSearchResults(os.Stdout, results)
}

func printSessionSearchResults(out io.Writer, results []session.SearchResult) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tUPDATED\tMODEL\tCWD\tTITLE\tMATCH")
	for _, result := range results {
		meta := result.Metadata
		title := meta.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			meta.ID,
			meta.UpdatedAt.Local().Format("2006-01-02 15:04"),
			meta.Model,
			shortPath(meta.CWD),
			title,
			result.Excerpt,
		)
	}
	_ = w.Flush()
}

type doctorStatus string

const (
	doctorPass doctorStatus = "pass"
	doctorWarn doctorStatus = "warn"
	doctorFail doctorStatus = "fail"
)

type doctorCheck struct {
	Status doctorStatus
	Name   string
	Detail string
}

func runDoctor(ctx context.Context) int {
	_ = ctx
	checks := doctorChecks()
	printDoctorChecks(checks)
	for _, check := range checks {
		if check.Status == doctorFail {
			return 1
		}
	}
	return 0
}

func doctorChecks() []doctorCheck {
	cfg, err := config.Load()
	if err != nil {
		return []doctorCheck{{Status: doctorFail, Name: "config", Detail: err.Error()}}
	}
	checks := []doctorCheck{
		{Status: doctorPass, Name: "config", Detail: "loaded " + cfg.Source()},
	}
	checks = append(checks, doctorProviderCheck(cfg))
	checks = append(checks, doctorCredentialCheck(cfg))
	checks = append(checks, doctorModelCheck(cfg))
	checks = append(checks, doctorSessionStoreCheck())
	checks = append(checks, doctorGitChecks()...)
	return checks
}

func doctorProviderCheck(cfg *config.Config) doctorCheck {
	switch cfg.Provider {
	case "anthropic", "openai", "openrouter", "google":
		return doctorCheck{Status: doctorPass, Name: "provider", Detail: cfg.Provider}
	default:
		return doctorCheck{Status: doctorFail, Name: "provider", Detail: fmt.Sprintf("unknown provider %q", cfg.Provider)}
	}
}

func doctorCredentialCheck(cfg *config.Config) doctorCheck {
	switch cfg.Provider {
	case "anthropic":
		return envCredentialCheck("ANTHROPIC_API_KEY")
	case "openrouter":
		return envCredentialCheck("OPENROUTER_API_KEY")
	case "google":
		return envCredentialCheck("GOOGLE_API_KEY")
	case "openai":
		if cfg.SubscriptionAuth() {
			store, err := auth.DefaultStore()
			if err != nil {
				return doctorCheck{Status: doctorFail, Name: "credentials", Detail: err.Error()}
			}
			if _, ok, err := store.Get(auth.ProviderOpenAICodex); err != nil {
				return doctorCheck{Status: doctorFail, Name: "credentials", Detail: "could not read OpenAI subscription credentials"}
			} else if !ok {
				return doctorCheck{Status: doctorFail, Name: "credentials", Detail: "run `neo login` for OpenAI subscription auth"}
			}
			return doctorCheck{Status: doctorPass, Name: "credentials", Detail: "OpenAI subscription credentials are present"}
		}
		return envCredentialCheck("OPENAI_API_KEY")
	default:
		return doctorCheck{Status: doctorFail, Name: "credentials", Detail: "provider is invalid"}
	}
}

func envCredentialCheck(name string) doctorCheck {
	if strings.TrimSpace(os.Getenv(name)) == "" {
		return doctorCheck{Status: doctorFail, Name: "credentials", Detail: "set " + name}
	}
	return doctorCheck{Status: doctorPass, Name: "credentials", Detail: name + " is set"}
}

func doctorModelCheck(cfg *config.Config) doctorCheck {
	if strings.TrimSpace(cfg.Model) == "" {
		return doctorCheck{Status: doctorFail, Name: "model", Detail: "model is empty"}
	}
	return doctorCheck{Status: doctorPass, Name: "model", Detail: cfg.Model}
}

func doctorSessionStoreCheck() doctorCheck {
	store, err := session.DefaultStore()
	if err != nil {
		return doctorCheck{Status: doctorFail, Name: "sessions", Detail: err.Error()}
	}
	dir := store.Dir()
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return doctorCheck{Status: doctorFail, Name: "sessions", Detail: dir + " is not a directory"}
		}
		return doctorCheck{Status: doctorPass, Name: "sessions", Detail: "store is available at " + shortPath(dir)}
	}
	if os.IsNotExist(err) {
		parent := filepath.Dir(dir)
		if _, parentErr := os.Stat(parent); parentErr != nil {
			return doctorCheck{Status: doctorWarn, Name: "sessions", Detail: "store will be created at " + shortPath(dir)}
		}
		return doctorCheck{Status: doctorWarn, Name: "sessions", Detail: "store does not exist yet at " + shortPath(dir)}
	}
	return doctorCheck{Status: doctorFail, Name: "sessions", Detail: err.Error()}
}

func doctorGitChecks() []doctorCheck {
	checks := make([]doctorCheck, 0, 2)
	if _, err := exec.LookPath("git"); err != nil {
		return []doctorCheck{
			{Status: doctorFail, Name: "git", Detail: "git executable not found in PATH"},
			{Status: doctorWarn, Name: "workspace", Detail: "git workspace check skipped"},
		}
	}
	checks = append(checks, doctorCheck{Status: doctorPass, Name: "git", Detail: "git executable found"})
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		checks = append(checks, doctorCheck{Status: doctorWarn, Name: "workspace", Detail: "current directory is not a git workspace"})
		return checks
	}
	checks = append(checks, doctorCheck{Status: doctorPass, Name: "workspace", Detail: "git root " + shortPath(strings.TrimSpace(string(out)))})
	return checks
}

func printDoctorChecks(checks []doctorCheck) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tCHECK\tDETAIL")
	for _, check := range checks {
		fmt.Fprintf(w, "%s\t%s\t%s\n", check.Status, check.Name, check.Detail)
	}
	_ = w.Flush()
}

func mustSessionStore() *session.Store {
	store, err := session.DefaultStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sessions: %v\n", err)
		os.Exit(1)
	}
	return store
}

func shortPath(path string) string {
	if path == "" {
		return "-"
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" && (path == home || strings.HasPrefix(path, home+string(os.PathSeparator))) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

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
