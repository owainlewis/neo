// Package llmtest provides test doubles for the llm.Provider interface.
package llmtest

import (
	"context"
	"fmt"

	"github.com/owainlewis/neo/internal/llm"
)

// FakeProvider returns scripted responses in order. Useful for driving the
// agent through multi-turn scenarios in tests.
type FakeProvider struct {
	Responses []llm.Response
	Calls     []llm.Request
	idx       int
}

func (f *FakeProvider) Name() string { return "fake" }

func (f *FakeProvider) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	f.Calls = append(f.Calls, req)
	if f.idx >= len(f.Responses) {
		return nil, fmt.Errorf("fake: no scripted response for call %d", f.idx+1)
	}
	r := f.Responses[f.idx]
	f.idx++
	return &r, nil
}

// Text builds a single-text-block response that ends the turn.
func Text(s string) llm.Response {
	return llm.Response{
		Content:    []llm.ContentBlock{{Type: "text", Text: s}},
		StopReason: "end_turn",
	}
}

// ToolUse builds a response with one tool_use block.
func ToolUse(id, name string, input map[string]any) llm.Response {
	return llm.Response{
		Content:    []llm.ContentBlock{{Type: "tool_use", ID: id, Name: name, Input: input}},
		StopReason: "tool_use",
	}
}
