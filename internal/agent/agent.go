package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/permission"
	"github.com/owainlewis/neo/internal/tools"
)

type EventKind string

const (
	EventAssistantText   EventKind = "assistant_text"
	EventToolCall        EventKind = "tool_call"
	EventToolResult      EventKind = "tool_result"
	EventDone            EventKind = "done"
	EventError           EventKind = "error"
	EventMaxTurnsReached EventKind = "max_turns_reached"
)

var ErrMaxTurns = errors.New("max turns reached")

// ErrMaxOutputTokens is returned when the model stops because it hit its
// output-token limit with no tool calls to continue on. Ending the turn (with
// the partial text) beats silently re-calling the provider until MaxTurns.
var ErrMaxOutputTokens = errors.New("response truncated: model hit its max output tokens limit")

type Event struct {
	Kind     EventKind
	Text     string
	Name     string
	Args     map[string]any
	MaxTurns int
	IsError  bool // set on EventToolResult when the tool returned an error
	Err      error
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
	System       string
	SystemBlocks []llm.SystemBlock
	Provider     llm.Provider
	Tools        *tools.Registry
	Policy       permission.Policy
	Compactor    compact.Compactor
	Approve      func(context.Context, ApprovalRequest) (bool, error)
	MaxTurns     int
	OnEvent      func(Event)
	Messages     []llm.Message
}

type Agent struct {
	cfg      Config
	messages []llm.Message
	usage    llm.Usage
}

func New(cfg Config) *Agent {
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = 100
	}
	if cfg.Tools == nil {
		cfg.Tools = tools.NewRegistry()
	}
	if cfg.Compactor == nil {
		cfg.Compactor = compact.NoCompaction{}
	}
	return &Agent{cfg: cfg, messages: cloneMessages(cfg.Messages)}
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

func (a *Agent) Model() string { return a.cfg.Model }

func (a *Agent) SetModel(model string) {
	if strings.TrimSpace(model) == "" {
		return
	}
	a.cfg.Model = strings.TrimSpace(model)
}

func (a *Agent) SetPermissionMode(mode string) error {
	switch permission.Mode(mode) {
	case permission.ModeAsk, permission.ModeTrusted, permission.ModeReadonly:
	default:
		return fmt.Errorf("unknown permission mode: %s", mode)
	}
	switch p := a.cfg.Policy.(type) {
	case permission.WorkspacePolicy:
		p.Mode = permission.Mode(mode)
		a.cfg.Policy = p
	case *permission.WorkspacePolicy:
		p.Mode = permission.Mode(mode)
	default:
		return fmt.Errorf("permission policy does not support runtime mode changes")
	}
	return nil
}

func (a *Agent) ReplaceTranscript(messages []llm.Message) {
	a.messages = cloneMessages(messages)
	a.usage = llm.Usage{}
}

func (a *Agent) Clear() {
	a.messages = nil
}

func (a *Agent) ToolSpecs() []llm.ToolSpec {
	return a.cfg.Tools.Specs()
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
	logx.Debug("agent turn queued",
		"model", a.cfg.Model,
		"messages_before", len(a.messages),
		"images", len(imagePaths),
		"text", logx.SafeString(text, 240),
	)
	a.messages = append(a.messages, llm.Message{Role: llm.RoleUser, Content: content})
	return a.run(ctx)
}

func (a *Agent) run(ctx context.Context) (string, error) {
	var finalText strings.Builder
	for turn := 0; turn < a.cfg.MaxTurns; turn++ {
		logx.Debug("agent turn start",
			"turn", turn+1,
			"max_turns", a.cfg.MaxTurns,
			"messages", len(a.messages),
			"provider", a.cfg.Provider.Name(),
			"model", a.cfg.Model,
		)
		compacted, err := a.cfg.Compactor.Compact(ctx, a.messages)
		if err != nil {
			logx.Debug("agent compaction error", "turn", turn+1, "error", err.Error())
			a.emit(Event{Kind: EventError, Err: err})
			return "", err
		}
		a.messages = compacted
		resp, err := a.cfg.Provider.Complete(ctx, llm.Request{
			Model:        a.cfg.Model,
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
		var toolResults []llm.ContentBlock
		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				if block.Text != "" {
					a.emit(Event{Kind: EventAssistantText, Text: block.Text})
					finalText.WriteString(block.Text)
					finalText.WriteString("\n")
				}
			case "tool_use":
				a.emit(Event{Kind: EventToolCall, Name: block.Name, Args: block.Input})
				out, isErr := a.runTool(ctx, block.Name, block.Input)
				out = capToolResultContent(out)
				a.emit(Event{Kind: EventToolResult, Name: block.Name, Text: out, IsError: isErr})
				toolResults = append(toolResults, llm.ContentBlock{
					Type:      "tool_result",
					ToolUseID: block.ID,
					Content:   out,
					IsError:   isErr,
				})
			}
		}

		a.messages = append(a.messages, assistantMsg)
		if len(toolResults) > 0 {
			a.messages = append(a.messages, llm.Message{Role: llm.RoleUser, Content: toolResults})
			continue
		}

		if resp.StopReason == "end_turn" || resp.StopReason == "stop_sequence" || resp.StopReason == "" {
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

func (a *Agent) runTool(ctx context.Context, name string, input map[string]any) (string, bool) {
	logx.Debug("tool call", "name", name, "args", logx.SafeAny(input))
	t, ok := a.cfg.Tools.Get(name)
	if !ok {
		logx.Debug("tool lookup failed", "name", name)
		return fmt.Sprintf("unknown tool: %s", name), true
	}
	if a.cfg.Policy != nil {
		decision := a.cfg.Policy.Decide(ctx, permission.Request{ToolName: name, Args: input})
		switch decision.Decision {
		case permission.Deny:
			if decision.Reason == "" {
				decision.Reason = "permission policy denied this tool call"
			}
			logx.Debug("tool denied", "name", name, "reason", decision.Reason)
			return decision.Reason, true
		case permission.Ask:
			if a.cfg.Approve == nil {
				logx.Debug("tool approval missing approver", "name", name)
				return "permission approval required but no approver is configured", true
			}
			approved, err := a.cfg.Approve(ctx, ApprovalRequest{
				ToolName: name,
				Args:     cloneInput(input),
				Reason:   decision.Reason,
				Preview:  Preview(name, input),
			})
			if err != nil {
				logx.Debug("tool approval error", "name", name, "error", err.Error())
				return fmt.Sprintf("approval error: %v", err), true
			}
			if !approved {
				logx.Debug("tool denied by user", "name", name)
				return "user denied this tool call", true
			}
			logx.Debug("tool approved", "name", name)
		}
	}
	out, err := t.Run(ctx, input)
	if err != nil {
		logx.Debug("tool error",
			"name", name,
			"error", err.Error(),
			"output", logx.PayloadValue(out),
		)
		return fmt.Sprintf("error: %v\n%s", err, out), true
	}
	logx.Debug("tool result", "name", name, "output", logx.PayloadValue(out))
	return out, false
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
