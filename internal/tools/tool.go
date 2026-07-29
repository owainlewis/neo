package tools

import (
	"context"
	"fmt"
	"sort"

	"github.com/owainlewis/neo/internal/llm"
)

type Tool interface {
	Name() string
	Spec() llm.ToolSpec
	Run(ctx context.Context, input map[string]any) (string, error)
}

// ParallelTool is an optional capability for tools that are safe to execute
// alongside other parallel-safe calls from the same model response. Tools
// that do not implement it are always serial. The model never controls this
// decision.
type ParallelTool interface {
	ParallelSafe(input map[string]any) bool
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

// ParallelSafe reports the tool's runtime-owned concurrency classification.
// Unknown and unclassified tools fail closed to serial execution.
func (r *Registry) ParallelSafe(name string, input map[string]any) bool {
	t, ok := r.Get(name)
	if !ok {
		return false
	}
	p, ok := t.(ParallelTool)
	return ok && p.ParallelSafe(input)
}

func (r *Registry) Specs() []llm.ToolSpec {
	names := r.Names()
	out := make([]llm.ToolSpec, 0, len(names))
	for _, name := range names {
		out = append(out, r.tools[name].Spec())
	}
	return out
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
