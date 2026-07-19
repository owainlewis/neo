package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

// Runner runs a fresh subagent in dir and streams its events.
type Runner interface {
	RunAgent(ctx context.Context, dir, input string, events chan<- AgentEvent) (string, error)
}

// AgentResult is the uniform envelope returned to the calling agent.
// Ok means "the subagent completed", not that its answer is correct. The
// caller must still judge the output's content.
type AgentResult struct {
	Ok     bool   `json:"ok"`
	Output string `json:"output"`
	Kind   string `json:"kind"`
	Took   string `json:"took"`
}

var ErrAgents = errors.New("denied: session agent cap reached")

// Supervisor enforces budgets, owns agent state, and tags every agent
// event with its node into one stream. It never interprets agent decisions.
type Supervisor struct {
	runner Runner
	budget Budget
	Events chan Event

	mu     sync.Mutex
	nodes  map[int]*Node
	nextID int
	agents int // subagents admitted in this session
}

func NewSupervisor(runner Runner, b Budget) *Supervisor {
	return &Supervisor{
		runner: runner,
		budget: b,
		Events: make(chan Event, 256),
		nodes:  map[int]*Node{},
	}
}

// RunAgentPrompt starts a fresh subagent with a self-contained prompt. This is
// the chat-native delegation path; the execution participates in the
// supervisor budgets.
func (s *Supervisor) RunAgentPrompt(ctx context.Context, dir, prompt string) AgentResult {
	start := time.Now()
	fail := func(msg string) AgentResult {
		return AgentResult{Ok: false, Output: msg, Kind: "agent", Took: "0s"}
	}
	if strings.TrimSpace(prompt) == "" {
		return fail("agent: missing required input: prompt")
	}
	id, err := s.admitAndRegister(prompt)
	if err != nil {
		return fail(err.Error())
	}
	s.attribute(id, AgentEvent{Kind: "start"})
	out, ok := s.runAgent(ctx, id, dir, prompt)
	s.finish(id, out, ok)
	return AgentResult{Ok: ok, Output: out, Kind: "agent",
		Took: time.Since(start).Round(time.Second).String()}
}

// admitAndRegister checks and reserves the session-wide agent count as one
// operation, so parallel delegation cannot pass the limit.
func (s *Supervisor) admitAndRegister(input string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.agents >= s.budget.MaxAgents {
		return 0, ErrAgents
	}

	s.nextID++
	id := s.nextID
	s.nodes[id] = &Node{ID: id, Task: clip(input, 60)}
	s.agents++
	return id, nil
}

func (s *Supervisor) runAgent(ctx context.Context, id int, dir, input string) (string, bool) {
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
	out, err := s.runner.RunAgent(cctx, dir, input, ch)
	close(ch)
	wg.Wait()
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return out + "\n[subagent hit its wall-clock limit]", false
		}
		return "subagent error: " + err.Error() + "\n" + out, false
	}
	if strings.TrimSpace(out) == "" {
		return "subagent error: subagent returned an empty result", false
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
	select {
	case s.Events <- Event{At: time.Now(), Node: id, Task: n.Task, Ev: ev}:
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
	kind := "done"
	if !ok {
		kind = "fail"
	}
	s.attribute(id, AgentEvent{Kind: kind, Body: clip(out, 100)})
}

// AgentTool exposes subagent delegation to the model through neo's tool registry.
type AgentTool struct {
	Sup *Supervisor
	Dir string
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
	var res AgentResult
	for attempt := 0; attempt <= maxRetries; attempt++ {
		res = t.Sup.RunAgentPrompt(ctx, t.Dir, prompt)
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
