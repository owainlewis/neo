package tools

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

// MaxBashOutputBytes bounds command output in memory while leaving enough of
// both ends to diagnose failures. The agent applies its own final transcript
// cap, but the tool must not buffer an unbounded command before reaching it.
const MaxBashOutputBytes = 256 * 1024

type Bash struct {
	Timeout time.Duration
	CWD     string
}

func (Bash) Name() string { return "bash" }

func (Bash) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "bash",
		Description: "Run a shell command via /bin/bash -c. Returns bounded combined stdout+stderr, retaining the start and end when truncated. Use for git, tests, builds, file inspection beyond Read.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Shell command to execute"},
			},
			"required": []string{"command"},
		},
	}
}

func (b Bash) Run(ctx context.Context, input map[string]any) (string, error) {
	cmd, err := mustString(input, "command")
	if err != nil {
		return "", err
	}
	timeout := b.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.Command("/bin/bash", "-c", cmd)
	if b.CWD != "" {
		c.Dir = b.CWD
	}
	configureProcessGroup(c)
	buf := newBoundedOutput(MaxBashOutputBytes)
	c.Stdout = &buf
	c.Stderr = &buf
	if err := c.Start(); err != nil {
		return buf.String(), fmt.Errorf("start bash command: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- c.Wait()
	}()

	var runErr error
	var ctxErr error
	select {
	case runErr = <-done:
	case <-ctx.Done():
		ctxErr = ctx.Err()
		killProcessGroup(c)
		runErr = <-done
	}

	out := buf.String()
	if ctxErr != nil {
		if ctxErr == context.DeadlineExceeded {
			return out, fmt.Errorf("bash command exceeded timeout %s: %w", timeout, ctxErr)
		}
		return out, fmt.Errorf("bash command cancelled: %w", ctxErr)
	}
	if runErr != nil {
		// Surface as an error so the agent marks the tool_result with is_error=true.
		// Keep the captured output in the message so the model can see what happened.
		if ee, ok := runErr.(*exec.ExitError); ok {
			return out, fmt.Errorf("exit %d", ee.ExitCode())
		}
		return out, runErr
	}
	return out, nil
}

type boundedOutput struct {
	mu      sync.Mutex
	limit   int
	headCap int
	head    []byte
	tail    []byte
	total   int
}

func newBoundedOutput(limit int) boundedOutput {
	headCap := limit / 2
	return boundedOutput{limit: limit, headCap: headCap}
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := len(p)
	b.total += n
	if remaining := b.headCap - len(b.head); remaining > 0 {
		keep := min(remaining, len(p))
		b.head = append(b.head, p[:keep]...)
		p = p[keep:]
	}
	if len(p) > 0 {
		// Reserve space for the truncation marker so String always remains
		// below the advertised limit.
		tailCap := max(0, b.limit-b.headCap-256)
		if len(p) >= tailCap {
			b.tail = append(b.tail[:0], p[len(p)-tailCap:]...)
		} else {
			b.tail = append(b.tail, p...)
			if len(b.tail) > tailCap {
				b.tail = append(b.tail[:0], b.tail[len(b.tail)-tailCap:]...)
			}
		}
	}
	return n, nil
}

func (b *boundedOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.total <= len(b.head)+len(b.tail) {
		return string(append(append([]byte(nil), b.head...), b.tail...))
	}
	omitted := b.total - len(b.head) - len(b.tail)
	marker := fmt.Sprintf("\n\n[bash output truncated: omitted %d bytes; showing first %d and last %d bytes]\n\n", omitted, len(b.head), len(b.tail))
	out := make([]byte, 0, len(b.head)+len(marker)+len(b.tail))
	out = append(out, b.head...)
	out = append(out, marker...)
	out = append(out, b.tail...)
	return string(out)
}
