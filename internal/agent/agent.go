package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/tools"
)

type EventKind string

const (
	EventAssistantText EventKind = "assistant_text"
	EventToolCall      EventKind = "tool_call"
	EventToolResult    EventKind = "tool_result"
	EventDone          EventKind = "done"
	EventError         EventKind = "error"
)

type Event struct {
	Kind    EventKind
	Text    string
	Name    string
	Args    map[string]any
	IsError bool // set on EventToolResult when the tool returned an error
	Err     error
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
	MaxTurns     int
	OnEvent      func(Event)
	Messages     []llm.Message
}

type Agent struct {
	cfg      Config
	messages []llm.Message
}

func New(cfg Config) *Agent {
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = 50
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

func (a *Agent) Transcript() []llm.Message { return cloneMessages(a.messages) }

func (a *Agent) Send(ctx context.Context, userText string) (string, error) {
	a.messages = append(a.messages, llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentBlock{{Type: "text", Text: userText}},
	})
	return a.run(ctx)
}

func (a *Agent) run(ctx context.Context) (string, error) {
	var finalText strings.Builder
	for turn := 0; turn < a.cfg.MaxTurns; turn++ {
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
	return strings.TrimSpace(finalText.String()), fmt.Errorf("max turns reached")
}

func (a *Agent) runTool(ctx context.Context, name string, input map[string]any) (string, bool) {
	t, ok := a.cfg.Tools.Get(name)
	if !ok {
		return fmt.Sprintf("unknown tool: %s", name), true
	}
	out, err := t.Run(ctx, input)
	if err != nil {
		return fmt.Sprintf("error: %v\n%s", err, out), true
	}
	return out, false
}

func cloneMessages(in []llm.Message) []llm.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]llm.Message, len(in))
	for i, msg := range in {
		out[i].Role = msg.Role
		if len(msg.Content) == 0 {
			continue
		}
		out[i].Content = make([]llm.ContentBlock, len(msg.Content))
		for j, block := range msg.Content {
			out[i].Content[j] = block
			if block.Input != nil {
				cp := make(map[string]any, len(block.Input))
				for k, v := range block.Input {
					cp[k] = v
				}
				out[i].Content[j].Input = cp
			}
		}
	}
	return out
}
