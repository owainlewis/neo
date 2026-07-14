package factory

import (
	"context"
	"fmt"
	"time"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/tools"
)

// defaultStepTools is what an agent step gets when its frontmatter declares
// no tools: observation only. A role is a tool set — granting write access
// must be explicit.
var defaultStepTools = []string{"bash", "read_file", "grep", "glob"}

const defaultStepMaxTurns = agent.DefaultMaxTurns

// AgentRunner runs agent steps on neo's core agent loop. Each step gets a
// fresh agent (amnesiac by design) with a registry filtered to the step's
// frontmatter tool list — role enforcement by construction, not prose.
type AgentRunner struct {
	Provider     llm.Provider
	DefaultModel string
	Root         string // workspace root; bounds file tools via permission policy
	BashTimeout  time.Duration

	// Mode is the permission mode child agents run under. Steps execute
	// autonomously (there is no approver inside a step), so "ask" cannot be
	// honored mid-step; but "readonly" must propagate — a readonly session
	// delegating a step must not gain write access through the side door.
	// Empty defaults to trusted (the standalone step CLI case).
	Mode permission.Mode

	// Sup is set after NewSupervisor. It is used only when a step explicitly
	// opts into the agent tool; dynamic chat subagents do not get nested
	// delegation by default.
	Sup *Supervisor
}

func (r *AgentRunner) RunAgentStep(ctx context.Context, step Step, dir, input string, nodeID int, events chan<- AgentEvent) (string, error) {
	model := step.Model
	if model == "" {
		model = r.DefaultModel
	}
	maxTurns := step.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultStepMaxTurns
	}

	mode := r.Mode
	if mode == "" || mode == permission.ModeAsk {
		mode = permission.ModeTrusted
	}
	ag := agent.New(agent.Config{
		Model:     model,
		System:    step.Prompt,
		Provider:  r.Provider,
		Tools:     r.registry(step, dir, nodeID),
		Policy:    permission.New(string(mode), r.Root),
		Compactor: compact.NewSummarizer(r.Provider, model),
		MaxTurns:  maxTurns,
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
	events <- AgentEvent{
		Kind: "usage",
		Body: fmt.Sprintf("tokens in=%d out=%d cached=%d", u.InputTokens, u.OutputTokens, u.CacheReadTokens),
	}
	return out, err
}

// registry builds the subagent's tool set. An empty frontmatter list means
// observation-only for legacy static steps; dynamic chat subagents pass an
// explicit tool list.
func (r *AgentRunner) registry(step Step, dir string, nodeID int) *tools.Registry {
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
		AgentTool{Sup: r.Sup, CallerNode: nodeID, Dir: dir},
	)
	allowed := step.Tools
	if len(allowed) == 0 {
		allowed = defaultStepTools
	}
	return all.Filter(allowed)
}

// translate maps core agent loop events onto the factory event stream.
// Tool results are dropped unless they errored — the call line is the
// interesting signal for a status display. agent calls are dropped too:
// the child subagent's own start event renders it.
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

// summarize renders a tool call as a one-line status: what the step is
// doing right now. (agent.Preview is the approval diff, not a summary —
// it's empty for most tools.)
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
