package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/auth"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/anthropic"
	"github.com/owainlewis/neo/internal/llm/openai"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/projectctx"
	"github.com/owainlewis/neo/internal/session"
	"github.com/owainlewis/neo/internal/skills"
	"github.com/owainlewis/neo/internal/tools"
	"github.com/owainlewis/neo/internal/tui"
	"github.com/owainlewis/neo/internal/workspace"
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
	case "sessions":
		listSessions(ctx)
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
  neo sessions       List saved chat sessions
  neo resume <id>    Resume a saved chat session
  neo login          Log in to an OpenAI ChatGPT/Codex subscription (device code)
  neo logout         Remove stored subscription credentials
  neo help           Show this help

CONFIG:
  Reads neo.yaml (cwd) → ~/.neo/config.yaml → embedded defaults.
  Select a backend with the "provider" key: "anthropic" (default) or "openai".

  ANTHROPIC_API_KEY    required when provider is "anthropic"
  OPENAI_API_KEY       required when provider is "openai" with api_key auth

  To use a ChatGPT subscription instead of an API key, set in neo.yaml:
    provider: openai
    openai_auth: subscription
  then run "neo login".`)
}

func newRegistry(cwd, root string) *tools.Registry {
	return tools.NewRegistry(
		tools.Bash{Timeout: 2 * time.Minute, CWD: cwd},
		tools.ReadFile{},
		tools.WriteFile{},
		tools.EditFile{},
		tools.Grep{Root: root},
		tools.Glob{Root: root},
	)
}

// chatSystem builds the chat agent's system prompt as ordered blocks: a stable,
// cacheable base (the static instructions plus the skill catalog) followed by
// dynamic project context (AGENTS.md) kept in its own, uncached block. Splitting
// it this way lets prompt caching reuse the base across turns and sessions while
// the project tail varies. Discovery errors are non-fatal — they warn and fall
// back to the blocks built so far rather than failing to start.
//
// It returns both the flattened string and the blocks so the agent can pass
// whichever a provider supports.
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

func mustProvider(cfg *config.Config) llm.Provider {
	var (
		prov llm.Provider
		err  error
	)
	switch cfg.Provider {
	case "openai":
		if cfg.SubscriptionAuth() {
			prov, err = newCodexProvider()
		} else {
			prov, err = openai.New()
		}
	case "anthropic", "":
		prov, err = anthropic.New()
	default:
		fmt.Fprintf(os.Stderr, "unknown provider %q (expected \"anthropic\" or \"openai\")\n", cfg.Provider)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return prov
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
	prov := mustProvider(cfg)

	cwd, _ := os.Getwd() // "" on failure → cwd-dependent capabilities are skipped
	root := workspace.Root(cwd)
	reg := newRegistry(cwd, root)

	if sess == nil {
		var err error
		sess, err = store.Create(ctx, session.Metadata{
			Source: session.DefaultSource,
			CWD:    cwd,
			Model:  cfg.Model,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "create session: %v\n", err)
			os.Exit(1)
		}
	}

	// Skills are loaded once: the catalog is advertised in the system prompt
	// (via chatSystem), and the same slice drives $name expansion in the TUI.
	sk := loadSkills(cfg, cwd)

	system, systemBlocks := chatSystem(cfg, cwd, sk)
	ag := agent.New(agent.Config{
		Model:        cfg.Model,
		System:       system,
		SystemBlocks: systemBlocks,
		Provider:     prov,
		Tools:        reg,
		Policy:       permission.New(cfg.Permissions.Mode, root),
		Messages:     sess.Messages,
	})

	saveSession := func() error {
		sess.Messages = ag.Transcript()
		sess.Metadata.CWD = cwd
		sess.Metadata.Model = ag.Model()
		return store.Save(ctx, sess)
	}

	if err := tui.Run(ctx, ag, cfg.Model, Version, sk,
		tui.WithAfterSend(saveSession),
		tui.WithPermissionMode(cfg.Permissions.Mode),
		tui.WithSessions(store, sess, func(resumed *session.Session) {
			sess = resumed
		}),
		tui.WithModelChoices(modelChoices(cfg)),
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func modelChoices(cfg *config.Config) []tui.ModelChoice {
	switch cfg.Provider {
	case "openai":
		if cfg.SubscriptionAuth() {
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
	default:
		return []tui.ModelChoice{
			{ID: "claude-opus-4-8", Name: "Claude Opus 4.8", Description: "Default Anthropic model"},
		}
	}
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
