package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/tools"
)

type fastParallelTool struct{}

func (fastParallelTool) Name() string { return "fast_parallel" }
func (fastParallelTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "fast_parallel", InputSchema: map[string]any{"type": "object"}}
}
func (fastParallelTool) ParallelSafe(map[string]any) bool { return true }
func (fastParallelTool) Run(context.Context, map[string]any) (string, error) {
	return "ok", nil
}

type countingMatcher struct {
	mu    sync.Mutex
	calls int
}

func (m *countingMatcher) Requires(string, map[string]any) bool {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return false
}

func TestAgent_ParallelApprovalMatchingRunsExactlyOnce(t *testing.T) {
	matcher := &countingMatcher{}
	tool := fastParallelTool{}
	prov := parallelResponse(tool.Name(), 2)
	ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(tool), RequiresApproval: matcher.Requires})
	if _, err := ag.Send(context.Background(), "inspect"); err != nil {
		t.Fatal(err)
	}
	matcher.mu.Lock()
	got := matcher.calls
	matcher.mu.Unlock()
	if got != 2 {
		t.Fatalf("approval matches = %d, want 2", got)
	}
}

func TestAgent_UnknownToolIsSerialBarrier(t *testing.T) {
	tool := fastParallelTool{}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{
			{Type: "tool_use", ID: "before_a", Name: tool.Name()},
			{Type: "tool_use", ID: "before_b", Name: tool.Name()},
			{Type: "tool_use", ID: "unknown", Name: "missing"},
			{Type: "tool_use", ID: "after_a", Name: tool.Name()},
			{Type: "tool_use", ID: "after_b", Name: tool.Name()},
		}, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}
	groups := 0
	ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(tool), OnEvent: func(e Event) {
		if e.Kind == EventParallelStart {
			groups++
		}
	}})
	if _, err := ag.Send(context.Background(), "inspect"); err != nil {
		t.Fatal(err)
	}
	if groups != 2 {
		t.Fatalf("parallel groups = %d, want two groups split by unknown tool", groups)
	}
	results := prov.Calls[1].Messages[2].Content
	if len(results) != 5 || !results[2].IsError {
		t.Fatalf("unknown barrier results = %#v", results)
	}
}

func TestAgent_TextBlockEndsParallelGroup(t *testing.T) {
	tool := fastParallelTool{}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{
			{Type: "tool_use", ID: "call_a", Name: tool.Name()},
			{Type: "text", Text: "between"},
			{Type: "tool_use", ID: "call_b", Name: tool.Name()},
		}, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}
	var kinds []EventKind
	ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(tool), OnEvent: func(e Event) {
		kinds = append(kinds, e.Kind)
	}})
	if _, err := ag.Send(context.Background(), "inspect"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range kinds {
		if kind == EventParallelStart {
			t.Fatalf("calls separated by text formed a parallel group: %v", kinds)
		}
	}
	want := []EventKind{EventToolCall, EventToolResult, EventAssistantCommentary, EventToolCall, EventToolResult}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("event order = %v, want prefix %v", kinds, want)
		}
	}
}

type barrierState struct {
	mu              sync.Mutex
	parallelRunning int
	serialDone      bool
	violated        bool
}

type barrierParallelTool struct{ state *barrierState }

func (barrierParallelTool) Name() string { return "barrier_parallel" }
func (barrierParallelTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "barrier_parallel", InputSchema: map[string]any{"type": "object"}}
}
func (barrierParallelTool) ParallelSafe(map[string]any) bool { return true }
func (t barrierParallelTool) Run(_ context.Context, input map[string]any) (string, error) {
	phase, _ := input["phase"].(string)
	t.state.mu.Lock()
	if phase == "after" && !t.state.serialDone {
		t.state.violated = true
	}
	t.state.parallelRunning++
	t.state.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	t.state.mu.Lock()
	t.state.parallelRunning--
	t.state.mu.Unlock()
	return "ok", nil
}

type barrierSerialTool struct{ state *barrierState }

func (barrierSerialTool) Name() string { return "barrier_serial" }
func (barrierSerialTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "barrier_serial", InputSchema: map[string]any{"type": "object"}}
}
func (t barrierSerialTool) Run(context.Context, map[string]any) (string, error) {
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	if t.state.parallelRunning != 0 {
		t.state.violated = true
	}
	t.state.serialDone = true
	return "ok", nil
}

func TestAgent_SerialToolIsBarrierBetweenParallelGroups(t *testing.T) {
	state := &barrierState{}
	parallel := barrierParallelTool{state: state}
	serial := barrierSerialTool{state: state}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{
			{Type: "tool_use", ID: "before_a", Name: parallel.Name(), Input: map[string]any{"phase": "before"}},
			{Type: "tool_use", ID: "before_b", Name: parallel.Name(), Input: map[string]any{"phase": "before"}},
			{Type: "tool_use", ID: "serial", Name: serial.Name()},
			{Type: "tool_use", ID: "after_a", Name: parallel.Name(), Input: map[string]any{"phase": "after"}},
			{Type: "tool_use", ID: "after_b", Name: parallel.Name(), Input: map[string]any{"phase": "after"}},
		}, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}
	ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(parallel, serial)})
	if _, err := ag.Send(context.Background(), "inspect"); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	violated := state.violated
	state.mu.Unlock()
	if violated {
		t.Fatal("serial barrier overlapped a parallel group or ran out of order")
	}
}

func TestAgent_EventCallbackIsSerializedDuringParallelExecution(t *testing.T) {
	tool := fastParallelTool{}
	prov := parallelResponse(tool.Name(), 8)
	var active atomic.Int32
	var concurrent atomic.Bool
	ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(tool), OnEvent: func(Event) {
		if active.Add(1) != 1 {
			concurrent.Store(true)
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
	}})
	if _, err := ag.Send(context.Background(), "inspect"); err != nil {
		t.Fatal(err)
	}
	if concurrent.Load() {
		t.Fatal("event callback was invoked concurrently")
	}
}

func parallelResponse(toolName string, count int) *llmtest.FakeProvider {
	content := make([]llm.ContentBlock, count)
	for i := range content {
		content[i] = llm.ContentBlock{Type: "tool_use", ID: string(rune('a' + i)), Name: toolName}
	}
	return &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: content, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}
}
