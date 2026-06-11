// Package factory turns neo into a software factory: an AI orchestrator
// plans and delegates via a single tool (run_step); steps are either agent
// prompts or scripts, indistinguishable to the caller; a Go supervisor
// enforces budgets and streams every event to the UI; GitHub Issues hold
// all durable state (the steps talk to it with gh via bash).
//
// Division of labor:
//
//	agents decide    — planning, sequencing, triage, judgment
//	code  constrains — depth, fanout, agent count, time
//	code  observes   — events, attribution, rendering
//	code  never interprets — no state machines over agent outcomes
package factory

import (
	"strings"
	"sync"
	"time"
)

// Budget is the cage. Enforced by the runtime regardless of what any agent
// asks for. Agent-count is tree-wide; the rest are per node.
type Budget struct {
	MaxDepth      int           // orchestrator=0, workers=1, sub-workers=2
	MaxChildren   int           // per node
	MaxAgents     int           // tree-wide cap on agent steps
	MaxWall       time.Duration // per agent step
	ScriptTimeout time.Duration // per script step
}

// DefaultBudget is a short leash suitable for early supervised runs.
func DefaultBudget() Budget {
	return Budget{
		MaxDepth:      3,
		MaxChildren:   8,
		MaxAgents:     20,
		MaxWall:       15 * time.Minute,
		ScriptTimeout: 2 * time.Minute,
	}
}

// AgentEvent is one observation from a running step. Lifecycle kinds frame
// each node: "start" when it registers, then "done" (completed) or "fail"
// (errored, timed out, denied) exactly once at the end. Everything in
// between ("tool", "text", "error", "usage") is status.
type AgentEvent struct {
	Kind string `json:"kind"` // "start" | "tool" | "text" | "error" | "usage" | "done" | "fail"
	Body string `json:"body,omitempty"`
}

// Event is the attributed stream: every agent event tagged with its node
// and the node's place in the tree, so a consumer can reconstruct the whole
// hierarchy from the stream alone. One channel; the UI is a fold over it;
// events.jsonl is a tee of it.
type Event struct {
	At     time.Time  `json:"at"`
	Node   int        `json:"node"`
	Parent int        `json:"parent,omitempty"`
	Depth  int        `json:"depth,omitempty"`
	Step   string     `json:"step"`
	Task   string     `json:"task,omitempty"`
	Ev     AgentEvent `json:"ev"`
}

// Node is one step execution in the tree — agent or script.
type Node struct {
	ID      int
	Parent  int // 0 = the root's virtual parent
	Step    string
	Kind    string // "agent" | "script"
	Task    string // clipped, for the UI
	Depth   int
	Started time.Time

	mu       sync.Mutex
	done     bool
	err      string
	lastLine string
	children []int
}

// NodeView is an immutable snapshot of a Node for rendering.
type NodeView struct {
	ID, Parent, Depth int
	Step, Kind, Task  string
	Done              bool
	Err, LastLine     string
	Elapsed           time.Duration
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
