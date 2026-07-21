package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/tools"
)

type EventKind string

const (
	EventAssistantText       EventKind = "assistant_text"
	EventAssistantCommentary EventKind = "assistant_commentary"
	EventParallelStart       EventKind = "parallel_start"
	EventToolCall            EventKind = "tool_call"
	EventToolResult          EventKind = "tool_result"
	EventSteeringApplied     EventKind = "steering_applied"
	EventDone                EventKind = "done"
	EventError               EventKind = "error"
	EventMaxTurnsReached     EventKind = "max_turns_reached"
)

var ErrMaxTurns = errors.New("max turns reached")

// ErrMaxOutputTokens is returned when the model stops because it hit its
// output-token limit with no tool calls to continue on. Ending the turn (with
// the partial text) beats silently re-calling the provider until MaxTurns.
var ErrMaxOutputTokens = errors.New("response truncated: model hit its max output tokens limit")

type Event struct {
	Kind      EventKind
	Text      string
	Name      string
	Args      map[string]any
	ToolUseID string
	GroupID   string
	GroupSize int
	GroupPos  int
	Calls     []ToolCallRef
	MaxTurns  int
	IsError   bool // set on EventToolResult when the tool returned an error
	Err       error
}

// ToolCallRef is the ordered, immutable summary announced before a parallel
// group starts. IDs are provider tool-use IDs and are opaque to consumers.
type ToolCallRef struct {
	ID   string
	Name string
	Args map[string]any
}

type ApprovalRequest struct {
	ToolName string
	Args     map[string]any
	Reason   string
	Preview  string
}

type Config struct {
	Model string
	// System is the flattened system prompt. SystemBlocks, when set, carries the
	// same prompt as ordered segments so the provider can place cache breakpoints;
	// the loop passes both so providers can use whichever they support.
	System           string
	SystemBlocks     []llm.SystemBlock
	Provider         llm.Provider
	Tools            *tools.Registry
	Policy           permission.Policy
	Compactor        compact.Compactor
	Approve          func(context.Context, ApprovalRequest) (bool, error)
	MaxTurns         int
	MaxParallelTools int
	OnEvent          func(Event)
	Messages         []llm.Message
	Usage            llm.Usage
}

type Agent struct {
	cfg      Config
	messages []llm.Message
	usage    llm.Usage

	backendMu sync.RWMutex

	steerMu      sync.Mutex
	steerActive  bool
	steerPending []string

	groupSeq uint64
}

// DefaultMaxTurns is a high safety fuse for runaway tool loops, not a normal
// work budget. Coding tasks can legitimately need many model/tool cycles; the
// model should usually stop because it is done, not because Neo reached this.
const DefaultMaxTurns = 500

const DefaultMaxParallelTools = 8

func New(cfg Config) *Agent {
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = DefaultMaxTurns
	}
	if cfg.MaxParallelTools <= 0 {
		cfg.MaxParallelTools = DefaultMaxParallelTools
	}
	if cfg.Tools == nil {
		cfg.Tools = tools.NewRegistry()
	}
	if cfg.Compactor == nil {
		cfg.Compactor = compact.NoCompaction{}
	}
	return &Agent{cfg: cfg, messages: cloneMessages(cfg.Messages), usage: cfg.Usage}
}

func (a *Agent) emit(e Event) {
	if a.cfg.OnEvent != nil {
		a.cfg.OnEvent(e)
	}
}

// SetEventHandler replaces the event callback. Useful when the consumer (e.g.
// a Bubble Tea program) isn't constructed until after the agent.
func (a *Agent) SetEventHandler(fn func(Event)) {
	a.cfg.OnEvent = fn
}

func (a *Agent) SetApprover(fn func(context.Context, ApprovalRequest) (bool, error)) {
	a.cfg.Approve = fn
}

func (a *Agent) Transcript() []llm.Message { return cloneMessages(a.messages) }

func (a *Agent) Model() string {
	a.backendMu.RLock()
	defer a.backendMu.RUnlock()
	return a.cfg.Model
}

// Backend returns a consistent provider/model pair for display and session
// persistence.
func (a *Agent) Backend() (string, string) {
	a.backendMu.RLock()
	defer a.backendMu.RUnlock()
	return a.cfg.Provider.Name(), a.cfg.Model
}

func (a *Agent) SetModel(model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	a.backendMu.Lock()
	a.cfg.Model = model
	a.backendMu.Unlock()
}

// SetBackend changes the provider, model, and compactor as one unit. Callers
// switch only between turns, but the lock also keeps diagnostic readers and
// race-enabled tests from observing a half-switched backend.
func (a *Agent) SetBackend(provider llm.Provider, model string, compactor compact.Compactor) error {
	model = strings.TrimSpace(model)
	if provider == nil {
		return fmt.Errorf("provider is required")
	}
	if model == "" {
		return fmt.Errorf("model is required")
	}
	if compactor == nil {
		compactor = compact.NoCompaction{}
	}
	a.backendMu.Lock()
	a.cfg.Provider = provider
	a.cfg.Model = model
	a.cfg.Compactor = compactor
	a.backendMu.Unlock()
	return nil
}

func (a *Agent) backend() (llm.Provider, string, compact.Compactor) {
	a.backendMu.RLock()
	defer a.backendMu.RUnlock()
	return a.cfg.Provider, a.cfg.Model, a.cfg.Compactor
}

func (a *Agent) ReplaceTranscript(messages []llm.Message) {
	a.messages = cloneMessages(messages)
	a.usage = llm.Usage{}
}

func (a *Agent) SetUsage(usage llm.Usage) {
	a.usage = usage
}

func (a *Agent) Clear() {
	a.messages = nil
	a.usage = llm.Usage{}
}

func (a *Agent) Usage() llm.Usage {
	return a.usage
}

// RunTool runs a built-in tool directly through the same permission and
// approval path used for model-requested tool calls. It emits the normal tool
// call/result events, but does not mutate the LLM transcript.
func (a *Agent) RunTool(ctx context.Context, name string, input map[string]any) (string, bool) {
	a.emit(Event{Kind: EventToolCall, Name: name, Args: cloneInput(input)})
	out, isErr := a.runTool(ctx, name, input)
	out = capToolResultContent(out)
	a.emit(Event{Kind: EventToolResult, Name: name, Text: out, IsError: isErr})
	return out, isErr
}

func (a *Agent) Send(ctx context.Context, userText string) (string, error) {
	return a.SendWith(ctx, userText, nil)
}

// SendWith sends a user turn that may carry image attachments alongside the
// text. Images are placed before the text block, which is how vision models
// expect a "here's an image, now my question" message to be ordered. Paths
// that can't be read are skipped with an inline note rather than aborting the
// turn — attachments are best-effort.
func (a *Agent) SendWith(ctx context.Context, userText string, imagePaths []string) (string, error) {
	var content []llm.ContentBlock
	var skipped []string
	for _, p := range imagePaths {
		blk, err := imageBlock(p)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s (%v)", p, err))
			continue
		}
		content = append(content, blk)
	}
	text := userText
	if len(skipped) > 0 {
		text = strings.TrimSpace(text + "\n\n[could not attach: " + strings.Join(skipped, "; ") + "]")
	}
	if text != "" {
		content = append(content, llm.ContentBlock{Type: "text", Text: text})
	}
	if len(content) == 0 {
		return "", nil
	}
	a.beginSteering()
	defer a.endSteering()
	_, model, _ := a.backend()
	logx.Debug("agent turn queued",
		"model", model,
		"messages_before", len(a.messages),
		"images", len(imagePaths),
		"text", logx.SafeString(text, 240),
	)
	a.messages = append(a.messages, llm.Message{Role: llm.RoleUser, Content: content})
	return a.run(ctx)
}

// Steer adds a user instruction to the active turn. The loop consumes it only
// after the current provider response and any requested tools have completed,
// preserving tool_use/tool_result pairing in the transcript.
func (a *Agent) Steer(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	if !a.steerActive {
		return false
	}
	a.steerPending = append(a.steerPending, text)
	return true
}

func (a *Agent) beginSteering() {
	a.steerMu.Lock()
	a.steerActive = true
	a.steerPending = nil
	a.steerMu.Unlock()
}

func (a *Agent) endSteering() {
	a.steerMu.Lock()
	a.steerActive = false
	a.steerPending = nil
	a.steerMu.Unlock()
}

// takeSteering drains pending instructions. When closeIfEmpty is true, an
// empty inbox is atomically closed so a late instruction cannot be accepted
// after the loop has decided to finish the turn.
func (a *Agent) takeSteering(closeIfEmpty bool) (pending []string, closed bool) {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	if len(a.steerPending) == 0 {
		if closeIfEmpty {
			a.steerActive = false
			return nil, true
		}
		return nil, false
	}
	pending = append([]string(nil), a.steerPending...)
	a.steerPending = nil
	return pending, false
}

func (a *Agent) run(ctx context.Context) (string, error) {
	var finalText strings.Builder
	for turn := 0; turn < a.cfg.MaxTurns; turn++ {
		provider, model, compactor := a.backend()
		logx.Debug("agent turn start",
			"turn", turn+1,
			"max_turns", a.cfg.MaxTurns,
			"messages", len(a.messages),
			"provider", provider.Name(),
			"model", model,
		)
		compacted, err := compactor.Compact(ctx, a.messages)
		if err != nil {
			logx.Debug("agent compaction error", "turn", turn+1, "error", err.Error())
			a.emit(Event{Kind: EventError, Err: err})
			return "", err
		}
		a.messages = compacted
		resp, err := provider.Complete(ctx, llm.Request{
			Model:        model,
			System:       a.cfg.System,
			SystemBlocks: a.cfg.SystemBlocks,
			Messages:     a.messages,
			Tools:        a.cfg.Tools.Specs(),
		})
		if err != nil {
			logx.Debug("agent provider error", "turn", turn+1, "error", err.Error())
			a.emit(Event{Kind: EventError, Err: err})
			return "", err
		}
		a.usage = addUsage(a.usage, resp.Usage)
		logx.Debug("agent provider response",
			"turn", turn+1,
			"stop_reason", resp.StopReason,
			"content_blocks", len(resp.Content),
			"usage", resp.Usage,
		)

		// Build the assistant message and any matching tool_results, but do not
		// commit either to the transcript until both are ready. This guarantees
		// the transcript never contains a tool_use without its tool_result, even
		// if a tool panics or an early return is added later.
		assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: resp.Content}
		toolResults, steering := a.processResponseContent(ctx, resp.Content, &finalText)

		a.messages = append(a.messages, assistantMsg)
		if len(toolResults) > 0 {
			if err := ctx.Err(); err != nil {
				a.messages = append(a.messages, llm.Message{Role: llm.RoleUser, Content: toolResults})
				logx.Debug("agent turn canceled after tool results", "turn", turn+1, "error", err.Error())
				a.emit(Event{Kind: EventError, Err: err})
				return strings.TrimSpace(finalText.String()), err
			}
			if len(steering) == 0 {
				steering, _ = a.takeSteering(false)
			}
			toolResults = a.appendSteering(toolResults, steering)
			a.messages = append(a.messages, llm.Message{Role: llm.RoleUser, Content: toolResults})
			continue
		}

		if resp.StopReason == "end_turn" || resp.StopReason == "stop_sequence" || resp.StopReason == "" {
			if err := ctx.Err(); err != nil {
				logx.Debug("agent turn canceled after provider response", "turn", turn+1, "error", err.Error())
				a.emit(Event{Kind: EventError, Err: err})
				return strings.TrimSpace(finalText.String()), err
			}
			steering, closed := a.takeSteering(true)
			if !closed {
				content := a.appendSteering(nil, steering)
				a.messages = append(a.messages, llm.Message{Role: llm.RoleUser, Content: content})
				continue
			}
			logx.Debug("agent turn complete", "turn", turn+1, "stop_reason", resp.StopReason)
			a.emit(Event{Kind: EventDone})
			return strings.TrimSpace(finalText.String()), nil
		}
		if resp.StopReason == "max_tokens" {
			logx.Debug("agent max output tokens", "turn", turn+1)
			a.emit(Event{Kind: EventError, Err: ErrMaxOutputTokens})
			return strings.TrimSpace(finalText.String()), ErrMaxOutputTokens
		}
	}
	logx.Debug("agent max turns reached", "max_turns", a.cfg.MaxTurns)
	a.emit(Event{Kind: EventMaxTurnsReached, MaxTurns: a.cfg.MaxTurns, Err: ErrMaxTurns})
	return strings.TrimSpace(finalText.String()), ErrMaxTurns
}

func skippedToolResults(content []llm.ContentBlock, reason string) []llm.ContentBlock {
	var results []llm.ContentBlock
	for _, block := range content {
		if block.Type != "tool_use" {
			continue
		}
		results = append(results, llm.ContentBlock{
			Type:      "tool_result",
			ToolUseID: block.ID,
			Content:   reason,
			IsError:   true,
		})
	}
	return results
}

type preparedToolCall struct {
	block     llm.ContentBlock
	tool      tools.Tool
	decision  permission.Result
	lookupErr string
	groupID   string
	groupSize int
	groupPos  int
}

type toolOutcome struct {
	text    string
	isError bool
}

func (a *Agent) processResponseContent(ctx context.Context, content []llm.ContentBlock, finalText *strings.Builder) ([]llm.ContentBlock, []string) {
	var results []llm.ContentBlock
	hasTools := hasToolUse(content)
	for i := 0; i < len(content); {
		block := content[i]
		if block.Type == "text" {
			if block.Text != "" {
				kind := EventAssistantText
				if hasTools {
					kind = EventAssistantCommentary
				}
				a.emit(Event{Kind: kind, Text: block.Text})
				finalText.WriteString(block.Text)
				finalText.WriteString("\n")
			}
			i++
			continue
		}
		if block.Type != "tool_use" {
			i++
			continue
		}

		if reason, steering := a.executionStop(ctx); reason != "" {
			results = append(results, skippedToolResults(content[i:], reason)...)
			return results, steering
		}

		if !a.cfg.Tools.ParallelSafe(block.Name, block.Input) {
			result := a.executeSerialBlock(ctx, block)
			results = append(results, result)
			i++
			if reason, steering := a.executionStop(ctx); reason != "" {
				results = append(results, skippedToolResults(content[i:], reason)...)
				return results, steering
			}
			continue
		}

		j := i
		for j < len(content) && content[j].Type == "tool_use" && a.cfg.Tools.ParallelSafe(content[j].Name, content[j].Input) {
			j++
		}
		prepared := make([]preparedToolCall, 0, j-i)
		for _, candidate := range content[i:j] {
			prepared = append(prepared, a.prepareTool(ctx, candidate))
		}
		processed, batchResults, steering := a.executePreparedCalls(ctx, prepared)
		results = append(results, batchResults...)
		if processed < len(prepared) || len(steering) > 0 || ctx.Err() != nil {
			reason := "skipped because the user steered the active turn"
			if ctx.Err() != nil {
				reason = "skipped because the active turn was canceled"
			}
			results = append(results, skippedToolResults(content[i+processed:], reason)...)
			return results, steering
		}
		i = j
	}
	return results, nil
}

// executePreparedCalls splits a run of parallel-safe calls around approval
// barriers. Permission decisions were made serially by prepareTool.
func (a *Agent) executePreparedCalls(ctx context.Context, calls []preparedToolCall) (int, []llm.ContentBlock, []string) {
	var results []llm.ContentBlock
	processed := 0
	for processed < len(calls) {
		if reason, steering := a.executionStop(ctx); reason != "" {
			return processed, results, steering
		}
		barrier := processed
		for barrier < len(calls) && calls[barrier].decision.Decision != permission.Ask {
			barrier++
		}
		if barrier > processed {
			segment := calls[processed:barrier]
			segmentResults := a.executePreparedGroup(ctx, segment)
			results = append(results, segmentResults...)
			processed = barrier
			if reason, steering := a.executionStop(ctx); reason != "" {
				return processed, results, steering
			}
		}
		if processed < len(calls) {
			call := calls[processed]
			results = append(results, a.executePreparedSerial(ctx, call))
			processed++
			if reason, steering := a.executionStop(ctx); reason != "" {
				return processed, results, steering
			}
		}
	}
	return processed, results, nil
}

func (a *Agent) executePreparedGroup(ctx context.Context, calls []preparedToolCall) []llm.ContentBlock {
	runnable := 0
	for _, call := range calls {
		if call.tool != nil && call.lookupErr == "" && call.decision.Decision == permission.Allow {
			runnable++
		}
	}
	if len(calls) < 2 || runnable < 2 {
		results := make([]llm.ContentBlock, 0, len(calls))
		for _, call := range calls {
			results = append(results, a.executePreparedSerial(ctx, call))
		}
		return results
	}

	a.groupSeq++
	groupID := fmt.Sprintf("parallel-%d", a.groupSeq)
	refs := make([]ToolCallRef, len(calls))
	for i, call := range calls {
		refs[i] = ToolCallRef{ID: call.block.ID, Name: call.block.Name, Args: cloneInput(call.block.Input)}
	}
	a.emit(Event{Kind: EventParallelStart, GroupID: groupID, GroupSize: len(calls), Calls: refs})
	for i, call := range calls {
		a.emit(toolEvent(EventToolCall, call.block, groupID, len(calls), i, "", false))
	}

	outcomes := make([]toolOutcome, len(calls))
	sem := make(chan struct{}, a.cfg.MaxParallelTools)
	var wg sync.WaitGroup
	for i, call := range calls {
		call.groupID = groupID
		call.groupSize = len(calls)
		call.groupPos = i
		if call.tool == nil || call.lookupErr != "" || call.decision.Decision != permission.Allow {
			outcomes[i] = a.runPreparedTool(ctx, call)
			continue
		}
		wg.Add(1)
		go func(i int, call preparedToolCall) {
			defer wg.Done()
			if ctx.Err() != nil {
				outcomes[i] = toolOutcome{text: "skipped because the active turn was canceled", isError: true}
				return
			}
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				if ctx.Err() != nil {
					outcomes[i] = toolOutcome{text: "skipped because the active turn was canceled", isError: true}
					return
				}
				outcomes[i] = a.runPreparedTool(ctx, call)
			case <-ctx.Done():
				outcomes[i] = toolOutcome{text: "skipped because the active turn was canceled", isError: true}
			}
		}(i, call)
	}
	wg.Wait()

	results := make([]llm.ContentBlock, len(calls))
	for i, call := range calls {
		outcome := outcomes[i]
		outcome.text = capToolResultContent(outcome.text)
		a.emit(toolEvent(EventToolResult, call.block, groupID, len(calls), i, outcome.text, outcome.isError))
		results[i] = toolResult(call.block.ID, outcome)
	}
	return results
}

func (a *Agent) executeSerialBlock(ctx context.Context, block llm.ContentBlock) llm.ContentBlock {
	return a.executePreparedSerial(ctx, a.prepareTool(ctx, block))
}

func (a *Agent) executePreparedSerial(ctx context.Context, call preparedToolCall) llm.ContentBlock {
	call.groupSize = 1
	a.emit(toolEvent(EventToolCall, call.block, "", 1, 0, "", false))
	outcome := a.runPreparedTool(ctx, call)
	outcome.text = capToolResultContent(outcome.text)
	a.emit(toolEvent(EventToolResult, call.block, "", 1, 0, outcome.text, outcome.isError))
	return toolResult(call.block.ID, outcome)
}

func toolEvent(kind EventKind, block llm.ContentBlock, groupID string, groupSize, groupPos int, text string, isError bool) Event {
	return Event{Kind: kind, Name: block.Name, Args: cloneInput(block.Input), ToolUseID: block.ID,
		GroupID: groupID, GroupSize: groupSize, GroupPos: groupPos, Text: text, IsError: isError}
}

func toolResult(id string, outcome toolOutcome) llm.ContentBlock {
	return llm.ContentBlock{Type: "tool_result", ToolUseID: id, Content: outcome.text, IsError: outcome.isError}
}

func (a *Agent) executionStop(ctx context.Context) (string, []string) {
	if ctx.Err() != nil {
		return "skipped because the active turn was canceled", nil
	}
	if pending, _ := a.takeSteering(false); len(pending) > 0 {
		return "skipped because the user steered the active turn", pending
	}
	return "", nil
}

func (a *Agent) appendSteering(content []llm.ContentBlock, steering []string) []llm.ContentBlock {
	for _, text := range steering {
		content = append(content, llm.ContentBlock{Type: "text", Text: text})
		a.emit(Event{Kind: EventSteeringApplied, Text: text})
	}
	return content
}

func hasToolUse(content []llm.ContentBlock) bool {
	for _, block := range content {
		if block.Type == "tool_use" {
			return true
		}
	}
	return false
}

func (a *Agent) runTool(ctx context.Context, name string, input map[string]any) (string, bool) {
	call := a.prepareTool(ctx, llm.ContentBlock{Type: "tool_use", Name: name, Input: input})
	outcome := a.runPreparedTool(ctx, call)
	return outcome.text, outcome.isError
}

// prepareTool performs lookup and the permission decision on the coordinator
// goroutine. Approval itself remains deferred so it can act as a serial
// execution barrier.
func (a *Agent) prepareTool(ctx context.Context, block llm.ContentBlock) preparedToolCall {
	call := preparedToolCall{block: block, decision: permission.Result{Decision: permission.Allow}}
	t, ok := a.cfg.Tools.Get(block.Name)
	if !ok {
		call.lookupErr = fmt.Sprintf("unknown tool: %s", block.Name)
		return call
	}
	call.tool = t
	if a.cfg.Policy != nil {
		call.decision = a.cfg.Policy.Decide(ctx, permission.Request{
			ToolName: block.Name,
			Args:     block.Input,
			ReadOnly: a.cfg.Tools.ReadOnly(block.Name, block.Input),
		})
	}
	return call
}

func (a *Agent) runPreparedTool(ctx context.Context, call preparedToolCall) toolOutcome {
	name, input := call.block.Name, call.block.Input
	ctx = tools.WithCallMetadata(ctx, tools.CallMetadata{
		ToolUseID: call.block.ID,
		GroupID:   call.groupID,
		GroupSize: call.groupSize,
		GroupPos:  call.groupPos,
	})
	logx.Debug("tool call", "name", name, "args", logx.SafeAny(input))
	if call.lookupErr != "" {
		logx.Debug("tool lookup failed", "name", name)
		return toolOutcome{text: call.lookupErr, isError: true}
	}
	if a.cfg.Policy != nil {
		decision := call.decision
		switch decision.Decision {
		case permission.Deny:
			if decision.Reason == "" {
				decision.Reason = "permission policy denied this tool call"
			}
			logx.Debug("tool denied", "name", name, "reason", decision.Reason)
			return toolOutcome{text: decision.Reason, isError: true}
		case permission.Ask:
			if a.cfg.Approve == nil {
				logx.Debug("tool approval missing approver", "name", name)
				return toolOutcome{text: "permission approval required but no approver is configured", isError: true}
			}
			approved, err := a.cfg.Approve(ctx, ApprovalRequest{
				ToolName: name,
				Args:     cloneInput(input),
				Reason:   decision.Reason,
				Preview:  Preview(name, input),
			})
			if err != nil {
				logx.Debug("tool approval error", "name", name, "error", err.Error())
				return toolOutcome{text: fmt.Sprintf("approval error: %v", err), isError: true}
			}
			if !approved {
				logx.Debug("tool denied by user", "name", name)
				return toolOutcome{text: "user denied this tool call", isError: true}
			}
			logx.Debug("tool approved", "name", name)
		}
	}
	out, err := call.tool.Run(ctx, input)
	if err != nil {
		logx.Debug("tool error",
			"name", name,
			"error", err.Error(),
			"output", logx.PayloadValue(out),
		)
		return toolOutcome{text: fmt.Sprintf("error: %v\n%s", err, out), isError: true}
	}
	logx.Debug("tool result", "name", name, "output", logx.PayloadValue(out))
	return toolOutcome{text: out}
}

func addUsage(a, b llm.Usage) llm.Usage {
	return llm.Usage{
		InputTokens:         a.InputTokens + b.InputTokens,
		OutputTokens:        a.OutputTokens + b.OutputTokens,
		CacheCreationTokens: a.CacheCreationTokens + b.CacheCreationTokens,
		CacheReadTokens:     a.CacheReadTokens + b.CacheReadTokens,
	}
}

func cloneMessages(in []llm.Message) []llm.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]llm.Message, len(in))
	for i, msg := range in {
		out[i].Role = msg.Role
		out[i].Content = cloneContentBlocks(msg.Content)
	}
	return out
}

func cloneContentBlocks(in []llm.ContentBlock) []llm.ContentBlock {
	if len(in) == 0 {
		return nil
	}
	out := make([]llm.ContentBlock, len(in))
	for i, block := range in {
		out[i] = block
		out[i].Input = cloneInput(block.Input)
	}
	return out
}

func cloneInput(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
