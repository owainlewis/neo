package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBash_SuccessReturnsOutput(t *testing.T) {
	out, err := Bash{}.Run(context.Background(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected output to contain 'hello', got %q", out)
	}
}

func TestBash_NonZeroExitReturnsError(t *testing.T) {
	// Regression: a failing command must surface as an error so the agent
	// can mark the tool_result with is_error=true. Previously the error was
	// silently swallowed into the output string.
	out, err := Bash{}.Run(context.Background(), map[string]any{"command": "echo nope; exit 7"})
	if err == nil {
		t.Fatalf("expected error for non-zero exit, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "7") {
		t.Fatalf("expected error to mention exit code 7, got %v", err)
	}
	if !strings.Contains(out, "nope") {
		t.Fatalf("expected captured output 'nope' to survive on failure, got %q", out)
	}
}

func TestBash_StderrIsCaptured(t *testing.T) {
	out, err := Bash{}.Run(context.Background(), map[string]any{"command": "echo err 1>&2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "err") {
		t.Fatalf("expected stderr in combined output, got %q", out)
	}
}

func TestBash_TimeoutFires(t *testing.T) {
	b := Bash{Timeout: 100 * time.Millisecond}
	out, err := b.Run(context.Background(), map[string]any{"command": "sleep 2"})
	if err == nil {
		t.Fatalf("expected timeout error, got nil (out=%q)", out)
	}
}

func TestBash_MissingCommand(t *testing.T) {
	_, err := Bash{}.Run(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}
