package tools

import (
	"context"
	"fmt"

	"github.com/owainlewis/neo/internal/llm"
)

type Tool interface {
	Name() string
	Spec() llm.ToolSpec
	Run(ctx context.Context, input map[string]any) (string, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry(ts ...Tool) *Registry {
	r := &Registry{tools: map[string]Tool{}}
	for _, t := range ts {
		r.tools[t.Name()] = t
	}
	return r
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Specs() []llm.ToolSpec {
	out := make([]llm.ToolSpec, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Spec())
	}
	return out
}

func (r *Registry) Filter(names []string) *Registry {
	if len(names) == 0 {
		return r
	}
	out := &Registry{tools: map[string]Tool{}}
	for _, n := range names {
		if t, ok := r.tools[n]; ok {
			out.tools[n] = t
		}
	}
	return out
}

func mustString(input map[string]any, key string) (string, error) {
	v, ok := input[key]
	if !ok {
		return "", fmt.Errorf("missing required input: %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("input %s must be a string", key)
	}
	return s, nil
}

func optString(input map[string]any, key string) string {
	v, ok := input[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// optInt accepts JSON numbers (float64), ints, or numeric strings; returns 0
// if absent or unparseable.
func optInt(input map[string]any, key string) int {
	v, ok := input[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
