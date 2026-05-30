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
	Model    string
	System   string
	Provider llm.Provider
	Tools    *tools.Registry
	MaxTurns int
	OnEvent  func(Event)
}

type Agent struct {
	cfg      Config
	messages []llm.Message
}

func New(cfg Config) *Agent {
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = 50
	}
	return &Agent{cfg: cfg}
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

func (a *Agent) Transcript() []llm.Message { return a.messages }

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
			Model:    a.cfg.Model,
			System:   a.cfg.System,
			Messages: a.messages,
			Tools:    a.cfg.Tools.Specs(),
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
