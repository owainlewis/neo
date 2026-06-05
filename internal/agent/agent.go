package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/owainlewis/neo/internal/compact"
	"github.com/owainlewis/neo/internal/llm"
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
		cfg.MaxTurns = 50
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

func (a *Agent) Clear() {
	a.messages = nil
}

func (a *Agent) ToolSpecs() []llm.ToolSpec {
	return a.cfg.Tools.Specs()
}

func (a *Agent) Usage() llm.Usage {
	return a.usage
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
	a.messages = append(a.messages, llm.Message{Role: llm.RoleUser, Content: content})
	return a.run(ctx)
}

func (a *Agent) run(ctx context.Context) (string, error) {
	var finalText strings.Builder
	for turn := 0; turn < a.cfg.MaxTurns; turn++ {
		compacted, err := a.cfg.Compactor.Compact(ctx, a.messages)
		if err != nil {
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
			a.emit(Event{Kind: EventError, Err: err})
			return "", err
		}
		a.usage = addUsage(a.usage, resp.Usage)

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
			a.emit(Event{Kind: EventDone})
			return strings.TrimSpace(finalText.String()), nil
		}
	}
	a.emit(Event{Kind: EventMaxTurnsReached, MaxTurns: a.cfg.MaxTurns, Err: ErrMaxTurns})
	return strings.TrimSpace(finalText.String()), ErrMaxTurns
}

func (a *Agent) runTool(ctx context.Context, name string, input map[string]any) (string, bool) {
	t, ok := a.cfg.Tools.Get(name)
	if !ok {
		return fmt.Sprintf("unknown tool: %s", name), true
	}
	if a.cfg.Policy != nil {
		decision := a.cfg.Policy.Decide(ctx, permission.Request{ToolName: name, Args: input})
		switch decision.Decision {
		case permission.Deny:
			if decision.Reason == "" {
				decision.Reason = "permission policy denied this tool call"
			}
			return decision.Reason, true
		case permission.Ask:
			if a.cfg.Approve == nil {
				return "permission approval required but no approver is configured", true
			}
			approved, err := a.cfg.Approve(ctx, ApprovalRequest{
				ToolName: name,
				Args:     cloneInput(input),
				Reason:   decision.Reason,
				Preview:  Preview(name, input),
			})
			if err != nil {
				return fmt.Sprintf("approval error: %v", err), true
			}
			if !approved {
				return "user denied this tool call", true
			}
		}
	}
	out, err := t.Run(ctx, input)
	if err != nil {
		return fmt.Sprintf("error: %v\n%s", err, out), true
	}
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
