package subagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/tools"
)

// Runner runs a fresh subagent with immutable per-run capabilities. Requiring
// options at the interface boundary prevents inspect calls from falling back
// to an unrestricted legacy execution path.
type Runner interface {
	RunAgentWithOptions(ctx context.Context, dir, input string, events chan<- AgentEvent, opts RunOptions) (string, error)
}

// AgentResult is the uniform envelope returned to the calling agent.
// Ok means "the subagent completed", not that its answer is correct.
type AgentResult struct {
	Ok     bool   `json:"ok"`
	Output string `json:"output"`
	Kind   string `json:"kind"`
	Took   string `json:"took"`
	Code   string `json:"code,omitempty"`
}

type AgentMode string

const (
	AgentModeWork    AgentMode = "work"
	AgentModeInspect AgentMode = "inspect"
)

var ErrAgents = errors.New("denied: session agent cap reached")

var (
	dynamicAgentTools = []string{"bash", "read_file", "write_file", "edit_file", "grep", "glob"}
	inspectAgentTools = []string{"read_file", "grep", "glob"}
)

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

// RunAgentPrompt starts a fresh subagent with a self-contained prompt.
func (s *Supervisor) RunAgentPrompt(ctx context.Context, dir, prompt string, options ...PromptOptions) AgentResult {
	start := time.Now()
	fail := func(msg, code string) AgentResult {
		return AgentResult{Ok: false, Output: msg, Kind: "agent", Took: "0s", Code: code}
	}
	if strings.TrimSpace(prompt) == "" {
		return fail("agent: missing required input: prompt", "invalid_input")
	}

	mode := AgentModeWork
	var call tools.CallMetadata
	if len(options) > 0 {
		mode = options[0].Mode
		call = options[0].Call
		if mode == "" {
			mode = AgentModeWork
		}
	}
	if mode != AgentModeWork && mode != AgentModeInspect {
		return fail(fmt.Sprintf("agent: invalid mode %q", mode), "invalid_input")
	}

	opts := RunOptions{Tools: append([]string(nil), dynamicAgentTools...)}
	if mode == AgentModeInspect {
		opts.Tools = append([]string(nil), inspectAgentTools...)
	}
	id, err := s.admitAndRegister(prompt, call, tools.ConversationGenerationFrom(ctx))
	if err != nil {
		return fail(err.Error(), "admission_denied")
	}
	s.attribute(id, AgentEvent{Kind: "start"})
	out, ok, code := s.runAgent(ctx, id, dir, prompt, opts)
	s.finish(id, out, ok)
	return AgentResult{Ok: ok, Output: out, Kind: "agent",
		Took: time.Since(start).Round(time.Second).String(), Code: code}
}

func (s *Supervisor) admitAndRegister(input string, call tools.CallMetadata, generation uint64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.agents >= s.budget.MaxAgents {
		return 0, ErrAgents
	}
	s.nextID++
	id := s.nextID
	s.nodes[id] = &Node{ID: id, Task: clip(input, 60), Call: call, Generation: generation}
	s.agents++
	return id, nil
}

func (s *Supervisor) runAgent(ctx context.Context, id int, dir, input string, opts RunOptions) (string, bool, string) {
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
	out, err := s.runner.RunAgentWithOptions(cctx, dir, input, ch, opts)
	close(ch)
	wg.Wait()
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return out + "\n[subagent hit its wall-clock limit]", false, "timeout"
		}
		if cctx.Err() != nil {
			return "subagent error: " + cctx.Err().Error() + "\n" + out, false, "canceled"
		}
		return "subagent error: " + err.Error() + "\n" + out, false, "execution_error"
	}
	if strings.TrimSpace(out) == "" {
		return "subagent error: subagent returned an empty result", false, "empty_result"
	}
	return out, true, ""
}

func (s *Supervisor) attribute(id int, ev AgentEvent) {
	s.mu.Lock()
	n := s.nodes[id]
	s.mu.Unlock()
	if n == nil {
		return
	}
	select {
	case s.Events <- Event{Generation: n.Generation, At: time.Now(), Node: id, Task: n.Task,
		CallID: n.Call.ToolUseID, GroupID: n.Call.GroupID,
		GroupSize: n.Call.GroupSize, GroupPos: n.Call.GroupPos, Ev: ev}:
	default: // never block agents on a slow UI
	}
}

func (s *Supervisor) finish(id int, out string, ok bool) {
	kind := "done"
	if !ok {
		kind = "fail"
	}
	s.attribute(id, AgentEvent{Kind: kind, Body: clip(out, 100)})
}

// AgentTool exposes subagent delegation to the model through Neo's tool registry.
type AgentTool struct {
	Sup *Supervisor
	Dir string
}

func (AgentTool) Name() string { return "agent" }

func (AgentTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name: "agent",
		Description: `Spawn a fresh subagent with a self-contained prompt; returns {"ok","output","kind","took","code"}.
The subagent has NO memory of this conversation — include everything it needs in the prompt.
ok=false: the subagent failed, timed out, or was denied (output says why; re-plan).
ok=true: it completed — judge the output's content yourself.
For independent investigations, issue several mode=inspect calls together. Use mode=work for one delegated change at a time.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string", "description": "Self-contained prompt for the subagent"},
				"mode":   map[string]any{"type": "string", "enum": []string{"work", "inspect"}, "description": "work (default, writable and serial) or inspect (read-only and parallel-safe)"},
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
	mode, err := parseAgentMode(input)
	if err != nil {
		return "", err
	}
	call, _ := tools.CallMetadataFrom(ctx)
	res := t.Sup.RunAgentPrompt(ctx, t.Dir, prompt, PromptOptions{Mode: mode, Call: call})
	return fmt.Sprintf("{\"ok\":%t,\"kind\":%q,\"took\":%q,\"code\":%q}\n%s",
		res.Ok, res.Kind, res.Took, res.Code, res.Output), nil
}

func (AgentTool) ParallelSafe(input map[string]any) bool { return isInspectInput(input) }

func isInspectInput(input map[string]any) bool {
	mode, err := parseAgentMode(input)
	return err == nil && mode == AgentModeInspect && strings.TrimSpace(stringInput(input, "prompt")) != ""
}

func stringInput(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return value
}

func parseAgentMode(input map[string]any) (AgentMode, error) {
	raw, ok := input["mode"]
	if !ok || raw == nil {
		return AgentModeWork, nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("agent: mode must be a string")
	}
	mode := AgentMode(value)
	switch mode {
	case AgentModeWork, AgentModeInspect:
		return mode, nil
	default:
		return "", fmt.Errorf("agent: invalid mode %q", value)
	}
}
