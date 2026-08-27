package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/owainlewis/neo/internal/llm"
)

type serialTestTool struct{}

func (serialTestTool) Name() string { return "serial" }
func (serialTestTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "serial", InputSchema: map[string]any{"type": "object"}}
}
func (serialTestTool) Run(context.Context, map[string]any) (string, error) { return "", nil }

type conditionalParallelTestTool struct{}

func (conditionalParallelTestTool) Name() string { return "conditional" }
func (conditionalParallelTestTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Name: "conditional", InputSchema: map[string]any{"type": "object"}}
}
func (conditionalParallelTestTool) Run(context.Context, map[string]any) (string, error) {
	return "", nil
}
func (conditionalParallelTestTool) ParallelSafe(input map[string]any) bool {
	safe, _ := input["safe"].(bool)
	return safe
}

func TestRegistryParallelSafeFailsClosed(t *testing.T) {
	r := NewRegistry(serialTestTool{}, conditionalParallelTestTool{})
	if r.ParallelSafe("missing", nil) {
		t.Fatal("unknown tool classified as parallel")
	}
	if r.ParallelSafe("serial", nil) {
		t.Fatal("unclassified tool classified as parallel")
	}
	if r.ParallelSafe("conditional", map[string]any{"safe": false}) {
		t.Fatal("false dynamic classification ignored")
	}
	if !r.ParallelSafe("conditional", map[string]any{"safe": true}) {
		t.Fatal("parallel capability was not honored")
	}
}

func TestReadSearchToolsAreParallelSafe(t *testing.T) {
	for name, tool := range map[string]Tool{
		"read_file": ReadFile{},
		"grep":      Grep{},
		"glob":      Glob{},
	} {
		parallel, ok := tool.(ParallelTool)
		if !ok || !parallel.ParallelSafe(nil) {
			t.Fatalf("%s is not parallel-safe", name)
		}
	}
}

func TestParallelReadSearchToolsHonorCanceledContext(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name         string
		tool         Tool
		input        map[string]any
		needsRipgrep bool
	}{
		{name: "read_file", tool: ReadFile{}, input: map[string]any{"path": path}},
		{name: "grep", tool: Grep{Root: root}, input: map[string]any{"pattern": "hello"}, needsRipgrep: true},
		{name: "glob", tool: Glob{Root: root}, input: map[string]any{"pattern": "**/*.txt"}, needsRipgrep: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.needsRipgrep {
				requireRipgrep(t)
			}
			_, err := tt.tool.Run(ctx, tt.input)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context canceled", err)
			}
		})
	}
}
