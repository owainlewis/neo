package factory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/tools"
)

const dynamicAgentSystemPrompt = `You are a focused subagent spawned by Neo's chat coordinator.

You have no memory of the parent conversation except the prompt you receive. Follow that prompt exactly, use tools as needed, and return a concise report with evidence. Do not commit changes unless explicitly asked.`

// AgentRunner runs chat-spawned subagents on Neo's core agent loop. Each call
// gets a fresh agent with the standard coding tool set and no nested agent tool.
type AgentRunner struct {
	backendMu    sync.RWMutex
	Provider     llm.Provider
	DefaultModel string
	Root         string // workspace root; bounds file tools via permission policy
	BashTimeout  time.Duration

	// Mode is the permission mode child agents run under. They execute
	// autonomously (there is no approver inside a subagent), so "ask" cannot be
	// honored during a run; but "readonly" must propagate. Empty defaults to
	// trusted.
	Mode permission.Mode
}

// SetBackend updates the default provider and model used by future workers.
// Existing workers keep the backend snapshot they started with.
func (r *AgentRunner) SetBackend(provider llm.Provider, model string) error {
	model = strings.TrimSpace(model)
	if provider == nil {
		return fmt.Errorf("provider is required")
	}
	if model == "" {
		return fmt.Errorf("model is required")
	}
	r.backendMu.Lock()
	r.Provider = provider
	r.DefaultModel = model
	r.backendMu.Unlock()
	return nil
}

func (r *AgentRunner) backend() (llm.Provider, string) {
	r.backendMu.RLock()
	defer r.backendMu.RUnlock()
	return r.Provider, r.DefaultModel
}

func (r *AgentRunner) RunAgent(ctx context.Context, dir, input string, events chan<- AgentEvent) (string, error) {
	return r.RunAgentWithOptions(ctx, dir, input, events, RunOptions{})
}

// RunAgentWithOptions applies immutable per-run capabilities without mutating
// the shared runner used by concurrent inspect children.
func (r *AgentRunner) RunAgentWithOptions(ctx context.Context, dir, input string, events chan<- AgentEvent, opts RunOptions) (string, error) {
	provider, model := r.backend()

	mode := r.Mode
	if opts.PermissionMode != "" {
		mode = opts.PermissionMode
	}
	if mode == "" || mode == permission.ModeAsk {
		mode = permission.ModeTrusted
	}
	ag := agent.New(agent.Config{
		Model:     model,
		System:    dynamicAgentSystemPrompt,
		Provider:  provider,
		Tools:     r.registryWithOptions(dir, opts),
		Policy:    permission.New(string(mode), r.Root),
		Compactor: compact.NewSummarizer(provider, model),
		MaxTurns:  agent.DefaultMaxTurns,
		OnEvent: func(e agent.Event) {
			if ev, ok := translate(e); ok {
				select {
				case events <- ev:
				case <-ctx.Done():
				}
			}
		},
	})

	out, err := ag.Send(ctx, input)
	u := ag.Usage()
	select {
	case events <- AgentEvent{
		Kind: "usage",
		Body: fmt.Sprintf("tokens in=%d out=%d cached=%d", u.InputTokens, u.OutputTokens, u.CacheReadTokens),
	}:
	case <-ctx.Done():
	}
	return out, err
}

func (r *AgentRunner) registry(dir string) *tools.Registry {
	return r.registryWithOptions(dir, RunOptions{})
}

func (r *AgentRunner) registryWithOptions(dir string, opts RunOptions) *tools.Registry {
	bashTimeout := r.BashTimeout
	if bashTimeout <= 0 {
		bashTimeout = 2 * time.Minute
	}
	all := tools.NewRegistry(
		tools.Bash{Timeout: bashTimeout, CWD: dir},
		tools.ReadFile{},
		tools.WriteFile{},
		tools.EditFile{},
		tools.Grep{Root: r.Root},
		tools.Glob{Root: r.Root},
	)
	if len(opts.Tools) == 0 {
		return all
	}
	return all.Filter(opts.Tools)
}

// translate maps core agent loop events onto the factory event stream.
func translate(e agent.Event) (AgentEvent, bool) {
	switch e.Kind {
	case agent.EventAssistantText, agent.EventAssistantCommentary:
		return AgentEvent{Kind: "text", Body: e.Text}, true
	case agent.EventToolCall:
		if e.Name == "agent" {
			return AgentEvent{}, false
		}
		return AgentEvent{Kind: "tool", Body: summarize(e.Name, e.Args)}, true
	case agent.EventToolResult:
		if e.IsError {
			return AgentEvent{Kind: "tool", Body: e.Name + " error: " + e.Text}, true
		}
		return AgentEvent{}, false
	case agent.EventError, agent.EventMaxTurnsReached:
		msg := ""
		if e.Err != nil {
			msg = e.Err.Error()
		}
		return AgentEvent{Kind: "error", Body: msg}, true
	default:
		return AgentEvent{}, false
	}
}

func summarize(name string, args map[string]any) string {
	arg := func(key string) string { s, _ := args[key].(string); return s }
	switch name {
	case "bash":
		return "$ " + arg("command")
	case "read_file":
		return "read " + arg("path")
	case "write_file":
		return "write " + arg("path")
	case "edit_file":
		return "edit " + arg("path")
	case "grep":
		return "grep " + arg("pattern")
	case "glob":
		return "glob " + arg("pattern")
	}
	return name
}
