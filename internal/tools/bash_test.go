package tools

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

func TestBash_LargeOutputRetainsHeadAndTail(t *testing.T) {
	out, err := Bash{}.Run(context.Background(), map[string]any{
		"command": "printf HEAD; yes x | head -c 400000; printf TAIL",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) > MaxBashOutputBytes {
		t.Fatalf("output length = %d, want at most %d", len(out), MaxBashOutputBytes)
	}
	for _, want := range []string{"HEAD", "[bash output truncated:", "TAIL"} {
		if !strings.Contains(out, want) {
			t.Fatalf("bounded output missing %q", want)
		}
	}
}

func TestBash_FailedLargeOutputRetainsErrorTail(t *testing.T) {
	out, err := Bash{}.Run(context.Background(), map[string]any{
		"command": "yes x | head -c 400000; printf 'fatal: useful error' >&2; exit 9",
	})
	if err == nil {
		t.Fatal("expected command failure")
	}
	if !strings.Contains(out, "fatal: useful error") {
		t.Fatalf("output did not retain useful error tail: %q", out[len(out)-min(len(out), 200):])
	}
}

func TestBash_TimeoutFires(t *testing.T) {
	b := Bash{Timeout: 100 * time.Millisecond}
	out, err := b.Run(context.Background(), map[string]any{"command": "sleep 2"})
	if err == nil {
		t.Fatalf("expected timeout error, got nil (out=%q)", out)
	}
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "exceeded timeout") {
		t.Fatalf("expected actionable deadline error, got %v", err)
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
		// The shell's > redirect creates the file before echo writes to it, so
		// waiting on existence alone can cancel while it is still empty and
		// leave readPID with nothing to parse.
		for i := 0; i < 50; i++ {
			if info, err := os.Stat(pidFile); err == nil && info.Size() > 0 {
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

func TestBash_RequestedTimeoutOverridesDefault(t *testing.T) {
	// The point of the argument: a command the model knows is slow must
	// survive a default that would otherwise kill it.
	b := Bash{Timeout: 50 * time.Millisecond}
	out, err := b.Run(context.Background(), map[string]any{"command": "sleep 0.4; echo done", "timeout": float64(30)})
	if err != nil {
		t.Fatalf("unexpected error: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "done") {
		t.Fatalf("expected command to finish, got %q", out)
	}
}

func TestBash_RequestedTimeoutFires(t *testing.T) {
	out, err := Bash{}.Run(context.Background(), map[string]any{"command": "sleep 5", "timeout": float64(1)})
	if err == nil {
		t.Fatalf("expected timeout error, got nil (out=%q)", out)
	}
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "exceeded timeout 1s") {
		t.Fatalf("expected deadline error naming the requested timeout, got %v", err)
	}
}

func TestBash_TimeoutForResolution(t *testing.T) {
	b := Bash{Timeout: 30 * time.Second}
	cases := []struct {
		name  string
		input map[string]any
		want  time.Duration
	}{
		{"absent uses configured default", map[string]any{}, 30 * time.Second},
		{"null uses configured default", map[string]any{"timeout": nil}, 30 * time.Second},
		{"json number", map[string]any{"timeout": float64(90)}, 90 * time.Second},
		{"int", map[string]any{"timeout": 90}, 90 * time.Second},
		{"int64", map[string]any{"timeout": int64(90)}, 90 * time.Second},
		{"numeric string", map[string]any{"timeout": "90"}, 90 * time.Second},
		{"above ceiling clamps", map[string]any{"timeout": float64(4000)}, MaxBashTimeout},
		// Seconds beyond ~9.2e9 overflow a Duration, so these must be clamped
		// in the float domain or they come out negative and expire at once.
		{"overflowing value clamps", map[string]any{"timeout": 1e12}, MaxBashTimeout},
		{"infinity clamps", map[string]any{"timeout": "Inf"}, MaxBashTimeout},
		{"out of range string clamps", map[string]any{"timeout": "1e400"}, MaxBashTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.timeoutFor(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("timeoutFor = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestBash_TimeoutForRejectsUnusableValues(t *testing.T) {
	// Falling back to the default would kill a long command at two minutes
	// with an error that never mentions the ignored argument.
	// A positive value that truncates to a zero Duration belongs here too:
	// running with it would cancel the command before it started.
	for _, v := range []any{"soon", "5m", "", float64(0), float64(-5), "1e-400", 1e-10, "1e-10", math.NaN(), "NaN", true} {
		if got, err := (Bash{}).timeoutFor(map[string]any{"timeout": v}); err == nil {
			t.Fatalf("timeout %v: expected error, got %s", v, got)
		}
	}
}

func TestBash_ZeroTimeoutUsesPackageDefault(t *testing.T) {
	got, err := (Bash{}).timeoutFor(map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != DefaultBashTimeout {
		t.Fatalf("timeoutFor = %s, want %s", got, DefaultBashTimeout)
	}
}

func TestBash_TimeoutForKeepsAHostDefaultAboveTheCeiling(t *testing.T) {
	// A host that allows longer than the ceiling must not have a request for
	// more time resolve to less than saying nothing at all.
	b := Bash{Timeout: 15 * time.Minute}
	got, err := b.timeoutFor(map[string]any{"timeout": float64(900)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 15*time.Minute {
		t.Fatalf("timeoutFor = %s, want %s", got, 15*time.Minute)
	}
}

func TestBash_UnusableTimeoutFailsBeforeRunning(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	out, err := Bash{}.Run(context.Background(), map[string]any{
		"command": "touch " + shellQuote(marker),
		"timeout": "soon",
	})
	if err == nil {
		t.Fatalf("expected error for unusable timeout, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error should name the offending argument, got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("command ran despite an unusable timeout")
	}
}

func TestBash_OuterDeadlineIsNotBlamedOnTheRequestedTimeout(t *testing.T) {
	// The agent's run budget can expire first. Reporting the requested
	// timeout then tells the model a command it just started took 10 minutes.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	out, err := Bash{}.Run(ctx, map[string]any{"command": "sleep 5", "timeout": float64(600)})
	if err == nil {
		t.Fatalf("expected deadline error, got nil (out=%q)", out)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected a deadline error, got %v", err)
	}
	if !strings.Contains(err.Error(), "agent's deadline") {
		t.Fatalf("error should attribute the stop to the outer deadline, got %v", err)
	}
}

func TestBash_AgentDeadlineReportsTimeBeforeTheKill(t *testing.T) {
	// The elapsed time in that message has to be measured when the command is
	// stopped. Reaping a child that escaped the process group can take far
	// longer than the command ran, and reporting that total tells the model a
	// command killed on the spot was the slow one.
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is needed to detach a child from the process group")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	out, err := Bash{}.Run(ctx, map[string]any{"command": "setsid sleep 1 & sleep 5"})
	if err == nil {
		t.Fatalf("expected deadline error, got nil (out=%q)", out)
	}
	m := regexp.MustCompile(`stopped after ([0-9a-z.µ]+) by`).FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatalf("error does not report how long the command ran, got %v", err)
	}
	ran, parseErr := time.ParseDuration(m[1])
	if parseErr != nil {
		t.Fatalf("reported duration %q is unparseable: %v", m[1], parseErr)
	}
	if ran >= 500*time.Millisecond {
		t.Fatalf("reported %s, but the command was killed at the 100ms deadline: %v", ran, err)
	}
}

func TestBash_CommandTimeoutIsNotBlamedOnTheAgentDeadline(t *testing.T) {
	// Reaping can outlast the agent's budget: a child that left the process
	// group survives the kill and holds the output pipe open, so the wait
	// after the kill runs past the outer deadline. The command still died of
	// its own timeout, and the error has to say that.
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is needed to detach a child from the process group")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	b := Bash{Timeout: 100 * time.Millisecond}
	out, err := b.Run(ctx, map[string]any{"command": "setsid sleep 1 & sleep 5"})
	if err == nil {
		t.Fatalf("expected timeout error, got nil (out=%q)", out)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected a deadline error, got %v", err)
	}
	if !strings.Contains(err.Error(), "exceeded timeout 100ms") {
		t.Fatalf("error should blame the command's own timeout, got %v", err)
	}
}

func TestBash_SpecAdvertisesTimeout(t *testing.T) {
	spec := Bash{Timeout: 2 * time.Minute}.Spec()
	props, ok := spec.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("spec has no properties: %#v", spec.InputSchema)
	}
	prop, ok := props["timeout"].(map[string]any)
	if !ok {
		t.Fatalf("spec does not advertise timeout: %#v", props)
	}
	if prop["maximum"] != int(MaxBashTimeout.Seconds()) {
		t.Fatalf("advertised maximum = %v, want %d", prop["maximum"], int(MaxBashTimeout.Seconds()))
	}
	desc, _ := prop["description"].(string)
	if !strings.Contains(desc, "120") || !strings.Contains(desc, "600") {
		t.Fatalf("description should name the default and maximum, got %q", desc)
	}
}

func TestBash_SpecAdvertisesAHostDefaultAboveTheCeiling(t *testing.T) {
	// Advertising the ceiling here would tell the model its own default is
	// out of range.
	spec := Bash{Timeout: 15 * time.Minute}.Spec()
	props := spec.InputSchema["properties"].(map[string]any)
	prop := props["timeout"].(map[string]any)
	if prop["maximum"] != 900 {
		t.Fatalf("advertised maximum = %v, want 900", prop["maximum"])
	}
	if desc, _ := prop["description"].(string); !strings.Contains(desc, "900") {
		t.Fatalf("description should name the raised maximum, got %q", desc)
	}
}
