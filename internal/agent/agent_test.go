package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/session"
	"github.com/owainlewis/neo/internal/tools"
)

func newTestAgent(t *testing.T, prov llm.Provider, ts ...tools.Tool) *Agent {
	t.Helper()
	return New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(ts...),
	})
}

func TestAgent_SetBackendPublishesAtomicProviderModelPair(t *testing.T) {
	ag := New(Config{Provider: namedProvider("a"), Model: "a-model"})
	const iterations = 2_000
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if err := ag.SetBackend(namedProvider("a"), "a-model", compact.NoCompaction{}); err != nil {
				t.Errorf("SetBackend a: %v", err)
				return
			}
			if err := ag.SetBackend(namedProvider("b"), "b-model", compact.NoCompaction{}); err != nil {
				t.Errorf("SetBackend b: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations*2; i++ {
			provider, model := ag.Backend()
			if model != provider+"-model" {
				t.Errorf("observed mixed backend %s/%s", provider, model)
				return
			}
		}
	}()
	wg.Wait()
}

func TestAgent_SetBackendUsesNewProviderModelAndCompactor(t *testing.T) {
	oldProvider := &llmtest.FakeProvider{}
	newProvider := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("switched")}}
	compactor := &countingCompactor{}
	ag := New(Config{Provider: oldProvider, Model: "old-model"})
	if err := ag.SetBackend(newProvider, "new-model", compactor); err != nil {
		t.Fatal(err)
	}

	out, err := ag.Send(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "switched" || len(oldProvider.Calls) != 0 || len(newProvider.Calls) != 1 {
		t.Fatalf("switch result=%q old calls=%d new calls=%d", out, len(oldProvider.Calls), len(newProvider.Calls))
	}
	if newProvider.Calls[0].Model != "new-model" || compactor.calls != 1 {
		t.Fatalf("request model=%q compactor calls=%d", newProvider.Calls[0].Model, compactor.calls)
	}
}

type namedProvider string

func (p namedProvider) Name() string { return string(p) }

func (p namedProvider) Complete(context.Context, llm.Request) (*llm.Response, error) {
	return nil, fmt.Errorf("not used")
}

func TestAgent_DefaultMaxTurnsIsHighSafetyFuse(t *testing.T) {
	ag := New(Config{Provider: &llmtest.FakeProvider{}})
	if ag.cfg.MaxTurns != DefaultMaxTurns {
		t.Fatalf("default MaxTurns = %d, want %d", ag.cfg.MaxTurns, DefaultMaxTurns)
	}
}

func TestAgent_TextOnlyTurnReturnsText(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("hello world")}}
	ag := newTestAgent(t, prov)

	out, err := ag.Send(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello world" {
		t.Fatalf("expected 'hello world', got %q", out)
	}
	// One user message, one assistant message.
	if got := len(ag.Transcript()); got != 2 {
		t.Fatalf("expected 2 messages in transcript, got %d", got)
	}
}

// echoTool returns whatever was passed in the "text" field. Used to drive
// tool_use → tool_result cycles in tests.
type echoTool struct{}

func (echoTool) Name() string { return "echo" }
func (echoTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "echo", Description: "echo", InputSchema: map[string]any{"type": "object"}}
}
func (echoTool) Run(_ context.Context, in map[string]any) (string, error) {
	if s, ok := in["text"].(string); ok {
		return s, nil
	}
	return "", nil
}

type namedTool string

func (t namedTool) Name() string { return string(t) }
func (t namedTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: string(t), Description: string(t), InputSchema: map[string]any{"type": "object"}}
}
func (t namedTool) Run(context.Context, map[string]any) (string, error) { return "ok", nil }

type steeringBlockingTool struct {
	started chan struct{}
	release chan struct{}
}

func (steeringBlockingTool) Name() string { return "block" }
func (steeringBlockingTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "block", Description: "block", InputSchema: map[string]any{"type": "object"}}
}
func (t steeringBlockingTool) Run(ctx context.Context, _ map[string]any) (string, error) {
	close(t.started)
	select {
	case <-t.release:
		return "done", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type blockingFirstProvider struct {
	started   chan struct{}
	release   chan struct{}
	responses []llm.Response
	calls     []llm.Request
}

type recordingTool struct {
	name   string
	called *bool
}

type parallelProbeTool struct {
	started chan struct{}
	release chan struct{}
	once    *sync.Once
	mu      *sync.Mutex
	running *int
	max     *int
}

func (parallelProbeTool) Name() string { return "parallel_probe" }
func (parallelProbeTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "parallel_probe", Description: "probe", InputSchema: map[string]any{"type": "object"}}
}
func (parallelProbeTool) ParallelSafe(map[string]any) bool { return true }
func (t parallelProbeTool) Run(ctx context.Context, input map[string]any) (string, error) {
	t.mu.Lock()
	*t.running++
	if *t.running > *t.max {
		*t.max = *t.running
	}
	if *t.running >= 2 {
		t.once.Do(func() { close(t.started) })
	}
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		*t.running--
		t.mu.Unlock()
	}()
	select {
	case <-t.release:
		id, _ := input["id"].(string)
		return id, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type askPolicy struct{}

func (askPolicy) Decide(context.Context, permission.Request) permission.Result {
	return permission.Result{Decision: permission.Ask, Reason: "test approval"}
}

type approvalProbeTool struct {
	mu      *sync.Mutex
	running *int
	max     *int
}

type cancelQueueTool struct {
	started chan struct{}
	once    *sync.Once
	mu      *sync.Mutex
	calls   *int
}

func (cancelQueueTool) Name() string { return "cancel_queue" }
func (cancelQueueTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "cancel_queue", Description: "probe", InputSchema: map[string]any{"type": "object"}}
}
func (cancelQueueTool) ParallelSafe(map[string]any) bool { return true }
func (t cancelQueueTool) Run(ctx context.Context, _ map[string]any) (string, error) {
	t.mu.Lock()
	(*t.calls)++
	t.once.Do(func() { close(t.started) })
	t.mu.Unlock()
	<-ctx.Done()
	return "", ctx.Err()
}

func (approvalProbeTool) Name() string { return "approval_probe" }
func (approvalProbeTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "approval_probe", Description: "probe", InputSchema: map[string]any{"type": "object"}}
}
func (approvalProbeTool) ParallelSafe(map[string]any) bool { return true }
func (t approvalProbeTool) Run(context.Context, map[string]any) (string, error) {
	t.mu.Lock()
	*t.running++
	if *t.running > *t.max {
		*t.max = *t.running
	}
	t.mu.Unlock()
	t.mu.Lock()
	*t.running--
	t.mu.Unlock()
	return "ok", nil
}

func (t recordingTool) Name() string { return t.name }
func (t recordingTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: t.name, Description: t.name, InputSchema: map[string]any{"type": "object"}}
}
func (t recordingTool) Run(context.Context, map[string]any) (string, error) {
	*t.called = true
	return "mutated", nil
}

func (p *blockingFirstProvider) Name() string { return "blocking-first" }
func (p *blockingFirstProvider) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	p.calls = append(p.calls, req)
	if len(p.calls) == 1 {
		close(p.started)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if len(p.calls) > len(p.responses) {
		return nil, fmt.Errorf("no response for call %d", len(p.calls))
	}
	resp := p.responses[len(p.calls)-1]
	return &resp, nil
}

func TestAgent_ToolUseFollowedByText(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "echo", map[string]any{"text": "pong"}),
		llmtest.Text("done"),
	}}
	ag := newTestAgent(t, prov, echoTool{})

	out, err := ag.Send(context.Background(), "ping")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "done" {
		t.Fatalf("expected final text 'done', got %q", out)
	}

	// Transcript invariant: every assistant tool_use must be followed by a
	// user tool_result with a matching ToolUseID.
	assertToolUseResultsPaired(t, ag.Transcript())
}

func TestAgent_SteeringIsAppliedAfterToolBoundary(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "block", nil),
		llmtest.Text("redirected"),
	}}
	ag := newTestAgent(t, prov, steeringBlockingTool{started: started, release: release})
	done := make(chan error, 1)
	go func() {
		_, err := ag.Send(context.Background(), "start")
		done <- err
	}()

	<-started
	if !ag.Steer("inspect the fallback instead") {
		t.Fatal("active turn rejected steering")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("send: %v", err)
	}

	if got := len(prov.Calls); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}
	content := prov.Calls[1].Messages[2].Content
	if len(content) != 2 {
		t.Fatalf("continuation content = %#v, want tool result and steering text", content)
	}
	if content[0].Type != "tool_result" || content[0].ToolUseID != "call_1" {
		t.Fatalf("first continuation block = %#v, want paired tool result", content[0])
	}
	if content[1].Type != "text" || content[1].Text != "inspect the fallback instead" {
		t.Fatalf("second continuation block = %#v, want steering text", content[1])
	}
	assertToolUseResultsPaired(t, ag.Transcript())
	if ag.Steer("too late") {
		t.Fatal("completed turn accepted steering")
	}
}

func TestAgent_SteeringSkipsStaleSiblingTools(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	mutated := false
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{
			Content: []llm.ContentBlock{
				{Type: "tool_use", ID: "call_1", Name: "block"},
				{Type: "tool_use", ID: "call_2", Name: "mutate"},
			},
			StopReason: "tool_use",
		},
		llmtest.Text("redirected"),
	}}
	ag := newTestAgent(t, prov,
		steeringBlockingTool{started: started, release: release},
		recordingTool{name: "mutate", called: &mutated},
	)
	done := make(chan error, 1)
	go func() {
		_, err := ag.Send(context.Background(), "start")
		done <- err
	}()

	<-started
	if !ag.Steer("stop before mutating") {
		t.Fatal("active turn rejected steering")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("send: %v", err)
	}
	if mutated {
		t.Fatal("stale sibling tool ran after steering")
	}

	content := prov.Calls[1].Messages[2].Content
	if len(content) != 3 {
		t.Fatalf("continuation content = %#v, want two results and steering text", content)
	}
	if content[1].Type != "tool_result" || content[1].ToolUseID != "call_2" || !content[1].IsError || !strings.Contains(content[1].Content, "steered") {
		t.Fatalf("skipped sibling result = %#v", content[1])
	}
	if content[2].Type != "text" || content[2].Text != "stop before mutating" {
		t.Fatalf("steering block = %#v", content[2])
	}
	assertToolUseResultsPaired(t, ag.Transcript())
}

func TestAgent_ParallelSafeCallsOverlapAndKeepSourceOrder(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	running, maxRunning := 0, 0
	tool := parallelProbeTool{started: started, release: release, once: &once, mu: &mu, running: &running, max: &maxRunning}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{
			{Type: "tool_use", ID: "call_a", Name: tool.Name(), Input: map[string]any{"id": "A"}},
			{Type: "tool_use", ID: "call_b", Name: tool.Name(), Input: map[string]any{"id": "B"}},
		}, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}
	var events []Event
	ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(tool), OnEvent: func(e Event) {
		events = append(events, e)
	}})
	done := make(chan error, 1)
	go func() {
		_, err := ag.Send(context.Background(), "inspect")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("parallel calls did not overlap")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if maxRunning != 2 {
		t.Fatalf("max concurrent calls = %d, want 2", maxRunning)
	}
	results := prov.Calls[1].Messages[2].Content
	if len(results) != 2 || results[0].ToolUseID != "call_a" || results[0].Content != "A" || results[1].ToolUseID != "call_b" || results[1].Content != "B" {
		t.Fatalf("ordered results = %#v", results)
	}
	if len(events) < 5 || events[0].Kind != EventParallelStart || events[0].GroupSize != 2 {
		t.Fatalf("parallel events = %#v", events)
	}
	if events[1].Kind != EventToolCall || events[1].ToolUseID != "call_a" || events[2].Kind != EventToolCall || events[2].ToolUseID != "call_b" {
		t.Fatalf("call events lost source order: %#v", events[:3])
	}
	if events[3].Kind != EventToolResult || events[3].ToolUseID != "call_a" || events[4].Kind != EventToolResult || events[4].ToolUseID != "call_b" {
		t.Fatalf("result events lost source order: %#v", events[3:5])
	}
}

func TestAgent_ApprovalTurnsParallelSafeCallsIntoSerialBarriers(t *testing.T) {
	var mu sync.Mutex
	running, maxRunning := 0, 0
	tool := approvalProbeTool{mu: &mu, running: &running, max: &maxRunning}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{
			{Type: "tool_use", ID: "call_a", Name: tool.Name()},
			{Type: "tool_use", ID: "call_b", Name: tool.Name()},
		}, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}
	var events []Event
	ag := New(Config{
		Model: "test", Provider: prov, Tools: tools.NewRegistry(tool), Policy: askPolicy{},
		Approve: func(context.Context, ApprovalRequest) (bool, error) { return true, nil },
		OnEvent: func(e Event) { events = append(events, e) },
	})
	if _, err := ag.Send(context.Background(), "inspect"); err != nil {
		t.Fatal(err)
	}
	if maxRunning != 1 {
		t.Fatalf("approval-requiring calls overlapped: max=%d", maxRunning)
	}
	for _, event := range events {
		if event.Kind == EventParallelStart {
			t.Fatalf("approval-requiring calls formed a parallel group: %#v", event)
		}
	}
}

func TestAgent_ParallelConcurrencyIsBounded(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	running, maxRunning := 0, 0
	tool := parallelProbeTool{started: started, release: release, once: &once, mu: &mu, running: &running, max: &maxRunning}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{
			{Type: "tool_use", ID: "call_a", Name: tool.Name()},
			{Type: "tool_use", ID: "call_b", Name: tool.Name()},
			{Type: "tool_use", ID: "call_c", Name: tool.Name()},
		}, StopReason: "tool_use"},
		llmtest.Text("done"),
	}}
	ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(tool), MaxParallelTools: 2})
	done := make(chan error, 1)
	go func() {
		_, err := ag.Send(context.Background(), "inspect")
		done <- err
	}()
	<-started
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if maxRunning != 2 {
		t.Fatalf("max concurrent calls = %d, want cap 2", maxRunning)
	}
}

func TestAgent_SteeringAfterParallelGroupSkipsWriteBarrier(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	running, maxRunning := 0, 0
	probe := parallelProbeTool{started: started, release: release, once: &once, mu: &mu, running: &running, max: &maxRunning}
	mutated := false
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{
			{Type: "tool_use", ID: "call_a", Name: probe.Name(), Input: map[string]any{"id": "A"}},
			{Type: "tool_use", ID: "call_b", Name: probe.Name(), Input: map[string]any{"id": "B"}},
			{Type: "tool_use", ID: "call_write", Name: "mutate"},
		}, StopReason: "tool_use"},
		llmtest.Text("redirected"),
	}}
	ag := newTestAgent(t, prov, probe, recordingTool{name: "mutate", called: &mutated})
	done := make(chan error, 1)
	go func() {
		_, err := ag.Send(context.Background(), "start")
		done <- err
	}()
	<-started
	if !ag.Steer("do not write") {
		t.Fatal("steering rejected")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if mutated {
		t.Fatal("write barrier ran after steering")
	}
	resultBlocks := prov.Calls[1].Messages[2].Content
	if len(resultBlocks) != 4 || resultBlocks[2].ToolUseID != "call_write" || !resultBlocks[2].IsError || !strings.Contains(resultBlocks[2].Content, "steered") {
		t.Fatalf("steered results = %#v", resultBlocks)
	}
	assertToolUseResultsPaired(t, ag.Transcript())
}

func TestAgent_CancellationPairsParallelAndUnstartedCalls(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	running, maxRunning := 0, 0
	probe := parallelProbeTool{started: started, release: release, once: &once, mu: &mu, running: &running, max: &maxRunning}
	mutated := false
	prov := &llmtest.FakeProvider{Responses: []llm.Response{{
		Content: []llm.ContentBlock{
			{Type: "tool_use", ID: "call_a", Name: probe.Name()},
			{Type: "tool_use", ID: "call_b", Name: probe.Name()},
			{Type: "tool_use", ID: "call_write", Name: "mutate"},
		},
		StopReason: "tool_use",
	}}}
	ag := newTestAgent(t, prov, probe, recordingTool{name: "mutate", called: &mutated})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ag.Send(ctx, "start")
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("send error = %v, want context canceled", err)
	}
	if mutated {
		t.Fatal("unstarted write ran after cancellation")
	}
	transcript := ag.Transcript()
	assertToolUseResultsPaired(t, transcript)
	results := transcript[2].Content
	if len(results) != 3 || results[2].ToolUseID != "call_write" || !results[2].IsError || !strings.Contains(results[2].Content, "canceled") {
		t.Fatalf("canceled results = %#v", results)
	}
}

func TestAgent_CancellationDoesNotStartCallsWaitingForParallelSlot(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	calls := 0
	tool := cancelQueueTool{started: started, once: &once, mu: &mu, calls: &calls}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{{
		Content: []llm.ContentBlock{
			{Type: "tool_use", ID: "call_a", Name: tool.Name()},
			{Type: "tool_use", ID: "call_b", Name: tool.Name()},
		},
		StopReason: "tool_use",
	}}}
	ag := New(Config{Model: "test", Provider: prov, Tools: tools.NewRegistry(tool), MaxParallelTools: 1})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ag.Send(ctx, "start")
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("send error = %v, want context canceled", err)
	}
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("tool Run calls = %d, want only active call", gotCalls)
	}
	assertToolUseResultsPaired(t, ag.Transcript())
}

func TestAgent_SteeringContinuesTextOnlyResponse(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	prov := &blockingFirstProvider{
		started: started,
		release: release,
		responses: []llm.Response{
			llmtest.Text("initial answer"),
			llmtest.Text("revised answer"),
		},
	}
	ag := newTestAgent(t, prov)
	done := make(chan error, 1)
	go func() {
		_, err := ag.Send(context.Background(), "start")
		done <- err
	}()

	<-started
	if !ag.Steer("change direction") {
		t.Fatal("active turn rejected steering")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("send: %v", err)
	}

	if got := len(prov.calls); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}
	messages := prov.calls[1].Messages
	last := messages[len(messages)-1]
	if last.Role != llm.RoleUser || len(last.Content) != 1 || last.Content[0].Text != "change direction" {
		t.Fatalf("last continuation message = %#v, want steering user message", last)
	}
}

func TestAgent_StoresBoundedBashToolResultBeforeTranscript(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "bash", map[string]any{"command": "printf HEAD; printf '%300000s' ''; printf TAIL"}),
		llmtest.Text("done"),
	}}
	var events []Event
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(tools.Bash{}),
		OnEvent: func(e Event) {
			events = append(events, e)
		},
	})

	if _, err := ag.Send(context.Background(), "run noisy command"); err != nil {
		t.Fatalf("send: %v", err)
	}

	result := ag.Transcript()[2].Content[0].Content
	if len(result) > maxToolResultContentBytes {
		t.Fatalf("tool result stored %d bytes, want at most %d", len(result), maxToolResultContentBytes)
	}
	for _, want := range []string{"HEAD", "[bash output truncated:", "TAIL"} {
		if !strings.Contains(result, want) {
			t.Fatalf("bounded bash result missing %q", want)
		}
	}
	if !sawToolResultEventWithText(events, result) {
		t.Fatal("tool result event did not receive capped output")
	}

	providerResult := prov.Calls[1].Messages[2].Content[0].Content
	if providerResult != result {
		t.Fatal("provider did not receive capped transcript content")
	}
}

func TestAgent_LeavesSmallToolResultUnchanged(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "echo", map[string]any{"text": "small\noutput"}),
		llmtest.Text("done"),
	}}
	ag := newTestAgent(t, prov, echoTool{})

	if _, err := ag.Send(context.Background(), "ping"); err != nil {
		t.Fatalf("send: %v", err)
	}
	result := ag.Transcript()[2].Content[0].Content
	if result != "small\noutput" {
		t.Fatalf("tool result = %q, want original small output", result)
	}
}

func TestAgent_ToolResultTruncationMarkerShape(t *testing.T) {
	large := strings.Repeat("0123456789\n", (maxToolResultContentBytes/11)+100)
	capped := capToolResultContent(large)

	if len(capped) > maxToolResultContentBytes {
		t.Fatalf("capped output stored %d bytes, want at most %d", len(capped), maxToolResultContentBytes)
	}
	assertTruncationMarker(t, capped, len(large), countOutputLines(large))
	if !strings.Contains(capped, "\n\n[tool output truncated:") {
		t.Fatal("marker was not separated from retained output")
	}
}

func TestAgent_CappedToolResultPersistsToSession(t *testing.T) {
	large := strings.Repeat("x\n", (maxToolResultContentBytes/2)+100)
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "echo", map[string]any{"text": large}),
		llmtest.Text("done"),
	}}
	ag := newTestAgent(t, prov, echoTool{})
	if _, err := ag.Send(context.Background(), "ping"); err != nil {
		t.Fatalf("send: %v", err)
	}

	store := session.NewStore(t.TempDir())
	sess := &session.Session{
		Metadata: session.Metadata{ID: "sess_test", Model: "test-model"},
		Messages: ag.Transcript(),
	}
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loaded, err := store.Load(context.Background(), "sess_test")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	result := loaded.Messages[2].Content[0].Content
	if len(result) > maxToolResultContentBytes {
		t.Fatalf("persisted result stored %d bytes, want at most %d", len(result), maxToolResultContentBytes)
	}
	assertTruncationMarker(t, result, len(large), countOutputLines(large))
	if result == large {
		t.Fatal("session stored the original giant output")
	}
}

func TestAgent_RestoresTranscriptFromConfig(t *testing.T) {
	prior := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "first"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "text", Text: "first reply"}}},
	}
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("second reply")}}
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(),
		Messages: prior,
	})

	out, err := ag.Send(context.Background(), "second")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "second reply" {
		t.Fatalf("unexpected output: %q", out)
	}
	if got := len(prov.Calls[0].Messages); got != 3 {
		t.Fatalf("provider saw %d messages, want 3", got)
	}
	if got := prov.Calls[0].Messages[0].Content[0].Text; got != "first" {
		t.Fatalf("provider did not receive restored first message: %q", got)
	}
}

func TestAgent_TranscriptReturnsCopy(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("hello")}}
	ag := newTestAgent(t, prov)
	if _, err := ag.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	transcript := ag.Transcript()
	transcript[0].Content[0].Text = "mutated"
	if got := ag.Transcript()[0].Content[0].Text; got == "mutated" {
		t.Fatal("Transcript allowed caller to mutate agent state")
	}
}

func TestAgent_ReplaceTranscript(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok")}}
	ag := newTestAgent(t, prov)
	if _, err := ag.Send(context.Background(), "before"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	replacement := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "resumed"}}},
	}

	ag.ReplaceTranscript(replacement)
	got := ag.Transcript()

	if len(got) != 1 || got[0].Content[0].Text != "resumed" {
		t.Fatalf("unexpected transcript: %#v", got)
	}
	replacement[0].Content[0].Text = "mutated"
	if ag.Transcript()[0].Content[0].Text != "resumed" {
		t.Fatal("ReplaceTranscript kept caller-owned message storage")
	}
}

func TestAgent_ProviderErrorLeavesTranscriptClean(t *testing.T) {
	// First call returns OK with a tool_use; second call (after we feed the
	// tool result back) errors. The transcript must still satisfy the
	// tool_use/tool_result pairing invariant so a follow-up Send doesn't 400.
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "echo", map[string]any{"text": "x"}),
		// no second response → fake returns an error
	}}
	ag := newTestAgent(t, prov, echoTool{})

	_, err := ag.Send(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error from provider")
	}
	assertToolUseResultsPaired(t, ag.Transcript())
}

func TestAgent_MaxTurnsReturnsSentinelWithPartialText(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		textAndToolUse("first partial", "call_1"),
		textAndToolUse("second partial", "call_2"),
	}}
	var events []Event
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(echoTool{}),
		MaxTurns: 2,
		OnEvent: func(e Event) {
			events = append(events, e)
		},
	})

	out, err := ag.Send(context.Background(), "hi")
	if !errors.Is(err, ErrMaxTurns) {
		t.Fatalf("expected ErrMaxTurns, got %v", err)
	}
	if out != "first partial\nsecond partial" {
		t.Fatalf("expected accumulated partial text, got %q", out)
	}
	if got := len(prov.Calls); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}
	if !sawMaxTurnsEvent(events, 2) {
		t.Fatalf("missing max-turns event in %#v", events)
	}
	for _, text := range []string{"first partial", "second partial"} {
		if !sawEvent(events, EventAssistantCommentary, text) {
			t.Fatalf("missing commentary event for %q in %#v", text, events)
		}
	}
	assertToolUseResultsPaired(t, ag.Transcript())
}

func TestAgent_MaxOutputTokensEndsTurnWithPartialText(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{{
		Content:    []llm.ContentBlock{{Type: "text", Text: "truncated answer"}},
		StopReason: "max_tokens",
	}}}
	var events []Event
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		OnEvent: func(e Event) {
			events = append(events, e)
		},
	})

	out, err := ag.Send(context.Background(), "hi")
	if !errors.Is(err, ErrMaxOutputTokens) {
		t.Fatalf("expected ErrMaxOutputTokens, got %v", err)
	}
	if out != "truncated answer" {
		t.Fatalf("expected partial text, got %q", out)
	}
	// The loop must not silently re-call the provider on a max_tokens stop.
	if got := len(prov.Calls); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}
	sawErr := false
	for _, e := range events {
		if e.Kind == EventError && errors.Is(e.Err, ErrMaxOutputTokens) {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatalf("missing max-output-tokens error event in %#v", events)
	}
}

func TestAgent_MaxTokensWithToolCallsStillRunsTools(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{
			Content:    []llm.ContentBlock{{Type: "tool_use", ID: "call_1", Name: "echo", Input: map[string]any{"text": "pong"}}},
			StopReason: "max_tokens",
		},
		llmtest.Text("done"),
	}}
	ag := newTestAgent(t, prov, echoTool{})

	out, err := ag.Send(context.Background(), "ping")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "done" {
		t.Fatalf("expected 'done', got %q", out)
	}
	assertToolUseResultsPaired(t, ag.Transcript())
}

func TestAgent_ApprovalAllowsTool(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "echo", map[string]any{"text": "pong"}),
		llmtest.Text("done"),
	}}
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(echoTool{}),
		Policy:   permission.New("ask", "."),
		Approve: func(context.Context, ApprovalRequest) (bool, error) {
			return true, nil
		},
	})
	out, err := ag.Send(context.Background(), "ping")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if out != "done" {
		t.Fatalf("out = %q", out)
	}
	assertToolUseResultsPaired(t, ag.Transcript())
}

func TestAgent_ApprovalDenialBecomesToolError(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "echo", map[string]any{"text": "pong"}),
		llmtest.Text("done"),
	}}
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(echoTool{}),
		Policy:   permission.New("ask", "."),
		Approve: func(context.Context, ApprovalRequest) (bool, error) {
			return false, nil
		},
	})
	if _, err := ag.Send(context.Background(), "ping"); err != nil {
		t.Fatalf("send: %v", err)
	}
	msgs := ag.Transcript()
	result := msgs[2].Content[0]
	if !result.IsError || !strings.Contains(result.Content, "denied") {
		t.Fatalf("tool result = %+v, want denial error", result)
	}
}

func TestAgent_MissingApproverDeniesAskedTool(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "echo", map[string]any{"text": "pong"}),
		llmtest.Text("done"),
	}}
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(echoTool{}),
		Policy:   permission.New("ask", "."),
	})
	if _, err := ag.Send(context.Background(), "ping"); err != nil {
		t.Fatalf("send: %v", err)
	}
	msgs := ag.Transcript()
	result := msgs[2].Content[0]
	if !result.IsError || !strings.Contains(result.Content, "no approver") {
		t.Fatalf("tool result = %+v, want missing approver error", result)
	}
}

func TestAgent_RunToolUsesPolicyAndDoesNotMutateTranscript(t *testing.T) {
	var events []Event
	approvals := 0
	ag := New(Config{
		Model:    "test-model",
		Provider: &llmtest.FakeProvider{},
		Tools:    tools.NewRegistry(namedTool("bash")),
		Policy:   permission.New("ask", "."),
		Approve: func(context.Context, ApprovalRequest) (bool, error) {
			approvals++
			return true, nil
		},
		OnEvent: func(e Event) {
			events = append(events, e)
		},
	})

	out, isErr := ag.RunTool(context.Background(), "bash", map[string]any{"command": "date"})
	if isErr {
		t.Fatalf("RunTool returned error output: %q", out)
	}
	if out != "ok" {
		t.Fatalf("RunTool output = %q, want ok", out)
	}
	if approvals != 1 {
		t.Fatalf("approvals = %d, want 1", approvals)
	}
	if got := len(ag.Transcript()); got != 0 {
		t.Fatalf("RunTool mutated transcript with %d messages", got)
	}
	if len(events) != 2 || events[0].Kind != EventToolCall || events[1].Kind != EventToolResult {
		t.Fatalf("events = %#v, want tool call then result", events)
	}
}

func TestAgent_RunToolCapsReturnedOutput(t *testing.T) {
	ag := New(Config{
		Model:    "test-model",
		Provider: &llmtest.FakeProvider{},
		Tools:    tools.NewRegistry(echoTool{}),
	})

	out, isErr := ag.RunTool(context.Background(), "echo", map[string]any{"text": strings.Repeat("x", maxToolResultContentBytes+1)})
	if isErr {
		t.Fatalf("RunTool returned error output: %q", out)
	}
	if len(out) > maxToolResultContentBytes {
		t.Fatalf("RunTool output stored %d bytes, want at most %d", len(out), maxToolResultContentBytes)
	}
	assertTruncationMarker(t, out, maxToolResultContentBytes+1, 1)
}

func TestAgent_RunToolReadonlyDeniesBash(t *testing.T) {
	ag := New(Config{
		Model:    "test-model",
		Provider: &llmtest.FakeProvider{},
		Tools:    tools.NewRegistry(namedTool("bash")),
		Policy:   permission.New("readonly", "."),
		Approve: func(context.Context, ApprovalRequest) (bool, error) {
			t.Fatal("readonly should deny bash without asking")
			return false, nil
		},
	})

	out, isErr := ag.RunTool(context.Background(), "bash", map[string]any{"command": "date"})
	if !isErr || !strings.Contains(out, "readonly denied bash") {
		t.Fatalf("RunTool = (%q, %v), want readonly denial", out, isErr)
	}
}

func TestAgent_SetPermissionModeTrustedSkipsApproval(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "bash", map[string]any{"command": "date"}),
		llmtest.Text("done"),
	}}
	approvals := 0
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(namedTool("bash")),
		Policy:   permission.New("ask", "."),
		Approve: func(context.Context, ApprovalRequest) (bool, error) {
			approvals++
			return false, nil
		},
	})
	if err := ag.SetPermissionMode("trusted"); err != nil {
		t.Fatalf("set permission mode: %v", err)
	}
	if _, err := ag.Send(context.Background(), "ping"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if approvals != 0 {
		t.Fatalf("approval prompts = %d, want 0", approvals)
	}
	result := ag.Transcript()[2].Content[0]
	if result.IsError {
		t.Fatalf("tool result = %+v, want success", result)
	}
}

func TestAgent_SetPermissionModeReadonlyDeniesBash(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "bash", map[string]any{"command": "date"}),
		llmtest.Text("done"),
	}}
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(namedTool("bash")),
		Policy:   permission.New("ask", "."),
		Approve: func(context.Context, ApprovalRequest) (bool, error) {
			t.Fatal("readonly should deny bash without asking")
			return false, nil
		},
	})
	if err := ag.SetPermissionMode("readonly"); err != nil {
		t.Fatalf("set permission mode: %v", err)
	}
	if _, err := ag.Send(context.Background(), "ping"); err != nil {
		t.Fatalf("send: %v", err)
	}
	result := ag.Transcript()[2].Content[0]
	if !result.IsError || !strings.Contains(result.Content, "readonly denied bash") {
		t.Fatalf("tool result = %+v, want readonly denial", result)
	}
}

func TestAgent_AccumulatesUsage(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{{Type: "text", Text: "one"}}, StopReason: "end_turn", Usage: llm.Usage{InputTokens: 1, OutputTokens: 2, CacheCreationTokens: 3, CacheReadTokens: 4}},
	}}
	ag := newTestAgent(t, prov)
	if _, err := ag.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("send: %v", err)
	}
	want := llm.Usage{InputTokens: 1, OutputTokens: 2, CacheCreationTokens: 3, CacheReadTokens: 4}
	if got := ag.Usage(); got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
}

func TestAgent_RestoresAndContinuesUsage(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		{Content: []llm.ContentBlock{{Type: "text", Text: "next"}}, StopReason: "end_turn", Usage: llm.Usage{InputTokens: 1, OutputTokens: 2, CacheCreationTokens: 3, CacheReadTokens: 4}},
	}}
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(),
		Usage:    llm.Usage{InputTokens: 10, OutputTokens: 20, CacheCreationTokens: 30, CacheReadTokens: 40},
	})
	if got, want := ag.Usage(), (llm.Usage{InputTokens: 10, OutputTokens: 20, CacheCreationTokens: 30, CacheReadTokens: 40}); got != want {
		t.Fatalf("restored usage = %+v, want %+v", got, want)
	}
	if _, err := ag.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("send: %v", err)
	}
	want := llm.Usage{InputTokens: 11, OutputTokens: 22, CacheCreationTokens: 33, CacheReadTokens: 44}
	if got := ag.Usage(); got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
}

func TestAgent_ClearResetsUsage(t *testing.T) {
	ag := New(Config{
		Provider: &llmtest.FakeProvider{},
		Usage:    llm.Usage{InputTokens: 10, OutputTokens: 20, CacheCreationTokens: 30, CacheReadTokens: 40},
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "old"}}}},
	})
	ag.Clear()
	if len(ag.Transcript()) != 0 {
		t.Fatalf("clear left transcript: %#v", ag.Transcript())
	}
	if got := ag.Usage(); got != (llm.Usage{}) {
		t.Fatalf("clear left usage = %+v", got)
	}
}

func TestAgent_CancelledProviderReturnsContextCanceled(t *testing.T) {
	ag := newTestAgent(t, cancelProvider{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ag.Send(ctx, "hi")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestAgent_CancelledToolCommitsRecoverableToolResult(t *testing.T) {
	started := make(chan struct{})
	prov := &cancelAfterToolProvider{}
	ag := newTestAgent(t, prov, blockingTool{started: started})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		_, err := ag.Send(ctx, "run tool")
		errc <- err
	}()
	<-started
	cancel()
	err := <-errc
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	transcript := ag.Transcript()
	assertToolUseResultsPaired(t, transcript)
	if len(transcript) != 3 {
		t.Fatalf("transcript length = %d, want 3: %#v", len(transcript), transcript)
	}
	result := transcript[2].Content[0]
	if result.Type != "tool_result" || !result.IsError || !strings.Contains(result.Content, "context canceled") {
		t.Fatalf("tool result = %+v, want cancellation error", result)
	}
	if prov.calls != 1 {
		t.Fatalf("provider calls = %d, want no follow-up provider call after canceled tool result", prov.calls)
	}
}

func TestAgent_CancellationDoesNotPersistUnappliedSteering(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	prov := &llmtest.FakeProvider{Responses: []llm.Response{
		llmtest.ToolUse("call_1", "block", nil),
	}}
	applied := false
	ag := New(Config{
		Model:    "test-model",
		Provider: prov,
		Tools:    tools.NewRegistry(steeringBlockingTool{started: started, release: release}),
		OnEvent: func(e Event) {
			if e.Kind == EventSteeringApplied {
				applied = true
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ag.Send(ctx, "start")
		done <- err
	}()

	<-started
	if !ag.Steer("unapplied") {
		t.Fatal("active turn rejected steering")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("send error = %v, want context canceled", err)
	}
	if applied {
		t.Fatal("canceled steering emitted an applied event")
	}
	transcript := ag.Transcript()
	assertToolUseResultsPaired(t, transcript)
	if got := len(transcript[2].Content); got != 1 {
		t.Fatalf("canceled continuation has %d blocks, want only tool result: %#v", got, transcript[2].Content)
	}
	if transcript[2].Content[0].Type != "tool_result" {
		t.Fatalf("canceled continuation = %#v", transcript[2].Content)
	}
}

func TestAgent_CompactorRunsBeforeProvider(t *testing.T) {
	prov := &llmtest.FakeProvider{Responses: []llm.Response{llmtest.Text("ok")}}
	comp := &countingCompactor{}
	ag := New(Config{
		Model:     "test-model",
		Provider:  prov,
		Tools:     tools.NewRegistry(),
		Compactor: comp,
	})
	if _, err := ag.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if comp.calls != 1 {
		t.Fatalf("compactor calls = %d, want 1", comp.calls)
	}
}

func textAndToolUse(text, id string) llm.Response {
	return llm.Response{
		Content: []llm.ContentBlock{
			{Type: "text", Text: text},
			{Type: "tool_use", ID: id, Name: "echo", Input: map[string]any{"text": "again"}},
		},
		StopReason: "tool_use",
	}
}

func sawMaxTurnsEvent(events []Event, limit int) bool {
	for _, event := range events {
		if event.Kind == EventMaxTurnsReached && event.MaxTurns == limit && errors.Is(event.Err, ErrMaxTurns) {
			return true
		}
	}
	return false
}

func sawToolResultEventWithText(events []Event, text string) bool {
	for _, event := range events {
		if event.Kind == EventToolResult && event.Text == text {
			return true
		}
	}
	return false
}

func sawEvent(events []Event, kind EventKind, text string) bool {
	for _, event := range events {
		if event.Kind == kind && event.Text == text {
			return true
		}
	}
	return false
}

func assertTruncationMarker(t *testing.T, content string, originalBytes, originalLines int) {
	t.Helper()
	wantParts := []string{
		"[tool output truncated:",
		fmt.Sprintf("original %d bytes", originalBytes),
		fmt.Sprintf("across %d lines", originalLines),
		"showing first ",
		"Re-run the tool with narrower output",
	}
	for _, want := range wantParts {
		if !strings.Contains(content, want) {
			t.Fatalf("capped content missing marker part %q:\n%s", want, markerTail(content))
		}
	}
}

func markerTail(content string) string {
	const tailBytes = 400
	if len(content) <= tailBytes {
		return content
	}
	return content[len(content)-tailBytes:]
}

func assertToolUseResultsPaired(t *testing.T, msgs []llm.Message) {
	t.Helper()
	pendingIDs := map[string]bool{}
	for _, m := range msgs {
		for _, b := range m.Content {
			switch b.Type {
			case "tool_use":
				pendingIDs[b.ID] = true
			case "tool_result":
				delete(pendingIDs, b.ToolUseID)
			}
		}
	}
	if len(pendingIDs) > 0 {
		ids := make([]string, 0, len(pendingIDs))
		for id := range pendingIDs {
			ids = append(ids, id)
		}
		t.Fatalf("transcript has unmatched tool_use IDs: %s", strings.Join(ids, ","))
	}
}

type cancelProvider struct{}

func (cancelProvider) Name() string { return "cancel" }
func (cancelProvider) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return nil, ctx.Err()
}

type blockingTool struct {
	started chan struct{}
}

func (blockingTool) Name() string { return "block" }

func (blockingTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "block", Description: "block", InputSchema: map[string]any{"type": "object"}}
}

func (t blockingTool) Run(ctx context.Context, _ map[string]any) (string, error) {
	close(t.started)
	<-ctx.Done()
	return "partial output", ctx.Err()
}

type cancelAfterToolProvider struct {
	calls int
}

func (p *cancelAfterToolProvider) Name() string { return "cancel-after-tool" }

func (p *cancelAfterToolProvider) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	p.calls++
	if p.calls == 1 {
		return &llm.Response{
			Content:    []llm.ContentBlock{{Type: "tool_use", ID: "call_1", Name: "block"}},
			StopReason: "tool_use",
		}, nil
	}
	return nil, ctx.Err()
}

type countingCompactor struct {
	compact.NoCompaction
	calls int
}

func (c *countingCompactor) Compact(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
	c.calls++
	if len(messages) == 0 {
		return nil, fmt.Errorf("expected user message before compaction")
	}
	return messages, nil
}
