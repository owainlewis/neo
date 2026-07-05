package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

func TestBash_TimeoutStopsChildProcesses(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	b := Bash{Timeout: 100 * time.Millisecond}
	out, err := b.Run(context.Background(), map[string]any{
		"command": "sleep 10 & echo $! > " + shellQuote(pidFile) + "; wait",
	})
	if err == nil {
		t.Fatalf("expected timeout error, got nil (out=%q)", out)
	}
	pid := readPID(t, pidFile)
	waitForProcessExit(t, pid)
}

func TestBash_CancelStopsChildProcesses(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for i := 0; i < 50; i++ {
			if _, err := os.Stat(pidFile); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	out, err := Bash{Timeout: 5 * time.Second}.Run(ctx, map[string]any{
		"command": "sleep 10 & echo $! > " + shellQuote(pidFile) + "; wait",
	})
	if err == nil {
		t.Fatalf("expected cancellation error, got nil (out=%q)", out)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
	pid := readPID(t, pidFile)
	waitForProcessExit(t, pid)
}

func TestBash_MissingCommand(t *testing.T) {
	_, err := Bash{}.Run(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse pid: %v", err)
	}
	return pid
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d is still running", pid)
}
