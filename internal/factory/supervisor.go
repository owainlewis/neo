package factory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

// StepAgent runs a resolved agent step against an input in dir, streaming
// events, returning the final message. NodeID identifies the execution so
// child run_step calls attribute correctly.
type StepAgent interface {
	RunAgentStep(ctx context.Context, step Step, dir, input string, nodeID int, events chan<- AgentEvent) (string, error)
}

// StepResult is the uniform envelope returned to the calling agent.
// Ok means "the step completed", NOT "the answer is yes": scripts conflate
// the two via exit code (fine — they hold invariants); for agent steps the
// caller judges the output's content itself. Collapsing agent judgment into
// a boolean is the classic mistake this design exists to avoid.
type StepResult struct {
	Ok     bool   `json:"ok"`
	Output string `json:"output"`
	Kind   string `json:"kind"`
	Took   string `json:"took"`
}

var (
	ErrDepth    = errors.New("denied: max depth reached — do the work in this step or narrow it")
	ErrChildren = errors.New("denied: max children for this step")
	ErrAgents   = errors.New("denied: tree-wide agent cap reached")
)

// Supervisor enforces budgets, owns the node tree, and tags every agent
// event with its node into one stream. It never interprets agent decisions.
type Supervisor struct {
	agent    StepAgent
	budget   Budget
	resolver Resolver
	Events   chan Event

	mu     sync.Mutex
	nodes  map[int]*Node
	nextID atomic.Int64
	agents atomic.Int64 // agent steps spawned, tree-wide
}

func NewSupervisor(agent StepAgent, b Budget, resolver Resolver) *Supervisor {
	return &Supervisor{
		agent:    agent,
		budget:   b,
		resolver: resolver,
		Events:   make(chan Event, 256),
		nodes:    map[int]*Node{},
	}
}

// Run starts the root step with the user's goal and blocks until the tree
// finishes. Everything below the root happens through run_step calls the
// root agent makes. Close(Events) afterwards is the caller's choice.
func (s *Supervisor) Run(ctx context.Context, dir, rootStep, goal string) (string, error) {
	res := s.RunStep(ctx, 0, dir, rootStep, goal)
	if !res.Ok {
		return res.Output, fmt.Errorf("step %s failed", rootStep)
	}
	return res.Output, nil
}

// RunStep resolves and executes a named step on behalf of caller node.
// Both kinds become nodes in the tree (so the UI shows everything); only
// agent steps consume the agent budget. Denials and failures return
// as results with the reason — the calling agent reads why and re-plans.
func (s *Supervisor) RunStep(ctx context.Context, caller int, dir, name, input string) StepResult {
	start := time.Now()
	fail := func(msg, kind string) StepResult {
		return StepResult{Ok: false, Output: msg, Kind: kind, Took: "0s"}
	}

	if name == "list" {
		return StepResult{Ok: true, Kind: "builtin", Took: "0s",
			Output: "available steps: " + strings.Join(s.resolver.List(), ", ")}
	}
	step, err := s.resolver.Resolve(name)
	if err != nil {
		return fail(err.Error(), "missing")
	}
	if err := s.admit(caller, step.Kind); err != nil {
		return fail(err.Error(), step.Kind)
	}

	id := s.register(caller, name, step.Kind, input)
	s.attribute(id, AgentEvent{Kind: "start"})
	var out string
	var ok bool
	switch step.Kind {
	case "agent":
		out, ok = s.runAgent(ctx, id, step, dir, input)
	case "script":
		out, ok = s.runScript(ctx, id, step.Path, dir, input)
	}
	s.finish(id, out, ok)
	return StepResult{Ok: ok, Output: out, Kind: step.Kind,
		Took: time.Since(start).Round(time.Second).String()}
}

// admit is the cage: depth and fanout for every step; agent-count only for
// agent steps (scripts are cheap; their cost is wall time, capped by
// ScriptTimeout).
func (s *Supervisor) admit(caller int, kind string) error {
	s.mu.Lock()
	p := s.nodes[caller]
	s.mu.Unlock()
	depth, kids := 0, 0
	if p != nil {
		depth = p.Depth + 1
		p.mu.Lock()
		kids = len(p.children)
		p.mu.Unlock()
	}
	switch {
	case depth > s.budget.MaxDepth:
		return ErrDepth
	case kids >= s.budget.MaxChildren:
		return ErrChildren
	case kind == "agent" && int(s.agents.Load()) >= s.budget.MaxAgents:
		return ErrAgents
	}
	return nil
}

func (s *Supervisor) register(parent int, step, kind, input string) int {
	id := int(s.nextID.Add(1))
	depth := 0
	s.mu.Lock()
	if p := s.nodes[parent]; p != nil {
		depth = p.Depth + 1
		p.mu.Lock()
		p.children = append(p.children, id)
		p.mu.Unlock()
	}
	s.nodes[id] = &Node{ID: id, Parent: parent, Step: step, Kind: kind,
		Task: clip(input, 60), Depth: depth, Started: time.Now()}
	s.mu.Unlock()
	return id
}

func (s *Supervisor) runAgent(ctx context.Context, id int, step Step, dir, input string) (string, bool) {
	s.agents.Add(1)
	cctx, cancel := context.WithTimeout(ctx, s.budget.MaxWall)
	defer cancel()

	ch := make(chan AgentEvent, 64)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev := range ch {
			s.attribute(id, ev)
		}
	}()
	out, err := s.agent.RunAgentStep(cctx, step, dir, input, id, ch)
	close(ch)
	wg.Wait()
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return out + "\n[agent step hit its wall-clock limit]", false
		}
		return "agent step error: " + err.Error() + "\n" + out, false
	}
	return out, true
}

func (s *Supervisor) runScript(ctx context.Context, id int, path, dir, input string) (string, bool) {
	cctx, cancel := context.WithTimeout(ctx, s.budget.ScriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, path)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	out := buf.String()
	s.attribute(id, AgentEvent{Kind: "tool", Body: "script: " + filepath.Base(path)})
	if cctx.Err() == context.DeadlineExceeded {
		return out + "\n[step timed out]", false
	}
	return out, err == nil
}

func (s *Supervisor) attribute(id int, ev AgentEvent) {
	s.mu.Lock()
	n := s.nodes[id]
	s.mu.Unlock()
	if n == nil {
		return
	}
	n.mu.Lock()
	if ev.Body != "" {
		n.lastLine = clip(ev.Body, 100)
	}
	n.mu.Unlock()
	select {
	case s.Events <- Event{At: time.Now(), Node: id, Parent: n.Parent,
		Depth: n.Depth, Step: n.Step, Task: n.Task, Ev: ev}:
	default: // never block agents on a slow UI
	}
}

func (s *Supervisor) finish(id int, out string, ok bool) {
	s.mu.Lock()
	n := s.nodes[id]
	s.mu.Unlock()
	if n == nil {
		return
	}
	n.mu.Lock()
	n.done = true
	if !ok {
		n.err = clip(out, 100)
	}
	n.mu.Unlock()
	kind := "done"
	if !ok {
		kind = "fail"
	}
	s.attribute(id, AgentEvent{Kind: kind, Body: clip(out, 100)})
}

// Snapshot returns the tree for rendering; the UI calls it per frame.
func (s *Supervisor) Snapshot() []NodeView {
	s.mu.Lock()
	defer s.mu.Unlock()
	views := make([]NodeView, 0, len(s.nodes))
	for _, n := range s.nodes {
		n.mu.Lock()
		elapsed := time.Since(n.Started)
		views = append(views, NodeView{
			ID: n.ID, Parent: n.Parent, Depth: n.Depth, Step: n.Step,
			Kind: n.Kind, Task: n.Task, Done: n.done, Err: n.err,
			LastLine: n.lastLine, Elapsed: elapsed,
		})
		n.mu.Unlock()
	}
	return views
}

// RunStepTool exposes run_step to the model through neo's tool registry.
// Granted per role by step-prompt frontmatter. CallerNode is bound per
// agent-loop instance so children attribute correctly.
type RunStepTool struct {
	Sup        *Supervisor
	CallerNode int
	Dir        string
}

func (RunStepTool) Name() string { return "run_step" }

func (RunStepTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name: "run_step",
		Description: `Run a named step (a sub-agent or script) with an input; returns {"ok","output","kind","took"}.
The step has NO memory of this conversation — include everything it needs in input.
ok=false: the step failed, timed out, or was denied (output says why; re-plan).
ok=true: it completed — judge the output's content yourself.
Enumerate available steps with name "list".`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":  map[string]any{"type": "string", "description": "Bare step name, e.g. worker, verify, triage, checks, list"},
				"input": map[string]any{"type": "string", "description": "Self-contained task input for the step"},
			},
			"required": []string{"name"},
		},
	}
}

func (t RunStepTool) Run(ctx context.Context, input map[string]any) (string, error) {
	name, _ := input["name"].(string)
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("run_step: missing required input: name")
	}
	task, _ := input["input"].(string)
	res := t.Sup.RunStep(ctx, t.CallerNode, t.Dir, name, task)
	return fmt.Sprintf("{\"ok\":%t,\"kind\":%q,\"took\":%q}\n%s",
		res.Ok, res.Kind, res.Took, res.Output), nil
}
