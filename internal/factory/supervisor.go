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
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

// StepAgent runs a resolved agent prompt against an input in dir, streaming
// events, returning the final message. NodeID identifies the execution so
// child agent calls attribute correctly.
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
	nextID int
	agents int // agent steps admitted, tree-wide
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
// finishes. Close(Events) afterwards is the caller's choice.
func (s *Supervisor) Run(ctx context.Context, dir, rootStep, goal string) (string, error) {
	res := s.RunStep(ctx, 0, dir, rootStep, goal)
	if !res.Ok {
		return res.Output, fmt.Errorf("step %s failed", rootStep)
	}
	return res.Output, nil
}

// RunStep resolves and executes a named legacy step on behalf of caller node.
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
	id, err := s.admitAndRegister(caller, name, step.Kind, input)
	if err != nil {
		return fail(err.Error(), step.Kind)
	}
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

// RunAgentPrompt starts a fresh subagent with a self-contained prompt. This is
// the chat-native delegation path: no named/static step file is involved, but
// the execution still participates in the supervisor tree and budgets.
func (s *Supervisor) RunAgentPrompt(ctx context.Context, caller int, dir, prompt string) StepResult {
	start := time.Now()
	fail := func(msg string) StepResult {
		return StepResult{Ok: false, Output: msg, Kind: "agent", Took: "0s"}
	}
	if strings.TrimSpace(prompt) == "" {
		return fail("agent: missing required input: prompt")
	}
	step := Step{
		Name:   "agent",
		Kind:   "agent",
		Prompt: dynamicAgentSystemPrompt,
		Tools:  dynamicAgentTools,
	}
	id, err := s.admitAndRegister(caller, step.Name, step.Kind, prompt)
	if err != nil {
		return fail(err.Error())
	}
	s.attribute(id, AgentEvent{Kind: "start"})
	out, ok := s.runAgent(ctx, id, step, dir, prompt)
	s.finish(id, out, ok)
	return StepResult{Ok: ok, Output: out, Kind: step.Kind,
		Took: time.Since(start).Round(time.Second).String()}
}

const dynamicAgentSystemPrompt = `You are a focused subagent spawned by Neo's chat coordinator.

You have no memory of the parent conversation except the prompt you receive. Follow that prompt exactly, use tools as needed, and return a concise report with evidence. Do not commit changes unless explicitly asked.`

// dynamicAgentTools is the default role for chat-spawned subagents. It
// intentionally omits "agent" so subagents cannot spawn further subagents
// unless a future explicit role opts into that power.
var dynamicAgentTools = []string{"bash", "read_file", "write_file", "edit_file", "grep", "glob"}

// admitAndRegister is the cage: it checks and reserves depth, fanout, and the
// tree-wide agent count as one operation, so parallel delegation cannot pass a
// limit before another worker is registered.
func (s *Supervisor) admitAndRegister(parent int, step, kind, input string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p := s.nodes[parent]
	depth, kids := 0, 0
	if p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		depth = p.Depth + 1
		kids = len(p.children)
	}
	switch {
	case depth > s.budget.MaxDepth:
		return 0, ErrDepth
	case kids >= s.budget.MaxChildren:
		return 0, ErrChildren
	case kind == "agent" && s.agents >= s.budget.MaxAgents:
		return 0, ErrAgents
	}

	s.nextID++
	id := s.nextID
	if p != nil {
		p.children = append(p.children, id)
	}
	s.nodes[id] = &Node{ID: id, Parent: parent, Step: step, Kind: kind,
		Task: clip(input, 60), Depth: depth, Started: time.Now()}
	if kind == "agent" {
		s.agents++
	}
	return id, nil
}

func (s *Supervisor) runAgent(ctx context.Context, id int, step Step, dir, input string) (string, bool) {
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
	if strings.TrimSpace(out) == "" {
		return "agent step error: subagent returned an empty result", false
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
	if err != nil {
		// The exit/exec error is the caller's only re-planning signal when
		// the script wrote nothing useful — never swallow it.
		return out + "\n[script error: " + err.Error() + "]", false
	}
	return out, true
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

// AgentTool exposes subagent delegation to the model through neo's tool registry.
// CallerNode is bound per agent-loop instance so children attribute correctly.
type AgentTool struct {
	Sup        *Supervisor
	CallerNode int
	Dir        string
}

func (AgentTool) Name() string { return "agent" }

func (AgentTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name: "agent",
		Description: `Spawn a fresh subagent with a self-contained prompt; returns {"ok","output","kind","took"}.
The subagent has NO memory of this conversation — include everything it needs in the prompt.
ok=false: the subagent failed, timed out, or was denied (output says why; re-plan).
ok=true: it completed — judge the output's content yourself.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt":      map[string]any{"type": "string", "description": "Self-contained prompt for the subagent"},
				"max_retries": map[string]any{"type": "integer", "description": "Optional retry count for subagent execution failures only; does not judge task success"},
			},
			"required": []string{"prompt"},
		},
	}
}

func (t AgentTool) Run(ctx context.Context, input map[string]any) (string, error) {
	prompt, _ := input["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("agent: missing required input: prompt")
	}
	maxRetries := parseRetryCount(input["max_retries"])
	var res StepResult
	for attempt := 0; attempt <= maxRetries; attempt++ {
		res = t.Sup.RunAgentPrompt(ctx, t.CallerNode, t.Dir, prompt)
		if res.Ok || attempt == maxRetries {
			break
		}
	}
	return fmt.Sprintf("{\"ok\":%t,\"kind\":%q,\"took\":%q}\n%s",
		res.Ok, res.Kind, res.Took, res.Output), nil
}

func parseRetryCount(v any) int {
	var n int
	switch x := v.(type) {
	case int:
		n = x
	case int64:
		n = int(x)
	case float64:
		n = int(x)
	}
	if n < 0 {
		return 0
	}
	if n > 5 {
		return 5
	}
	return n
}
