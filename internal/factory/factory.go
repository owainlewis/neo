// Package factory supervises chat-spawned subagents. The public model-facing
// surface is the agent tool: the coordinator writes a self-contained prompt,
// the supervisor enforces agent/time budgets, and the UI receives events for
// live progress.
//
// Division of labor:
//
//	agents decide    — planning, sequencing, triage, judgment
//	code  constrains — agent count, time
//	code  observes   — events, attribution, rendering
//	code  never interprets — no state machines over agent outcomes
package factory

import (
	"strings"
	"time"

	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/tools"
)

// RunOptions are immutable capabilities for one child run.
type RunOptions struct {
	PermissionMode permission.Mode
	Tools          []string
}

// PromptOptions configure one chat-spawned child and carry parent call
// attribution without exposing scheduler metadata to the model.
type PromptOptions struct {
	Mode AgentMode
	Call tools.CallMetadata
}

// Budget is enforced by the runtime regardless of what an agent asks for.
type Budget struct {
	MaxAgents int           // session-wide cap on subagents
	MaxWall   time.Duration // per subagent
}

// DefaultBudget is a short leash suitable for early supervised runs.
func DefaultBudget() Budget {
	return Budget{
		MaxAgents: 20,
		MaxWall:   15 * time.Minute,
	}
}

// AgentEvent is one observation from a running agent. Lifecycle kinds frame
// each node: "start" when it registers, then "done" (completed) or "fail"
// (errored, timed out, denied) exactly once at the end. Everything in
// between ("tool", "text", "error", "usage") is status.
type AgentEvent struct {
	Kind string `json:"kind"` // "start" | "tool" | "text" | "error" | "usage" | "done" | "fail"
	Body string `json:"body,omitempty"`
}

// Event tags an agent event with the execution that produced it.
type Event struct {
	At        time.Time  `json:"at"`
	Node      int        `json:"node"`
	Task      string     `json:"task,omitempty"`
	CallID    string     `json:"call_id,omitempty"`
	GroupID   string     `json:"group_id,omitempty"`
	GroupSize int        `json:"group_size,omitempty"`
	GroupPos  int        `json:"group_pos,omitempty"`
	Ev        AgentEvent `json:"ev"`
}

// Node is one subagent execution.
type Node struct {
	ID   int
	Task string // clipped, for the UI
	Call tools.CallMetadata
}

// clip returns the first line of s, truncated to at most n runes.
func clip(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
