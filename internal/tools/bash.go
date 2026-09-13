package tools

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

// MaxBashOutputBytes bounds command output in memory while leaving enough of
// both ends to diagnose failures. Keeping it at MaxOutputBytes means bash does
// its own head+tail truncation rather than being cut to a prefix later, which
// would drop the error a failing command prints last.
const MaxBashOutputBytes = MaxOutputBytes

// DefaultBashTimeout bounds a command when neither the host nor the model asks
// for something else.
const DefaultBashTimeout = 2 * time.Minute

// MaxBashTimeout is the ceiling on a model-requested timeout. A cold build or a
// full test suite on a slow machine needs more than the default, but letting
// the model name any duration would let one call hold the agent indefinitely.
// Requests above the ceiling are clamped rather than rejected, so an
// over-eager number still runs the command instead of wasting a turn.
const MaxBashTimeout = 10 * time.Minute

type Bash struct {
	Timeout time.Duration
	CWD     string
}

func (Bash) Name() string { return "bash" }

func (b Bash) Spec() llm.ToolSpec {
	maxSeconds := int(b.maxTimeout().Seconds())
	return llm.ToolSpec{
		Name:        "bash",
		Description: "Run a shell command via /bin/bash -c. Returns bounded combined stdout+stderr, retaining the start and end when truncated. Use for git, tests, builds, file inspection beyond Read.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Shell command to execute"},
				"timeout": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Optional wall-clock limit in seconds (default %d, maximum %d). Raise it for a slow build or a full test suite.", int(b.defaultTimeout().Seconds()), maxSeconds),
					"minimum":     1,
					"maximum":     maxSeconds,
				},
			},
			"required": []string{"command"},
		},
	}
}

// defaultTimeout is the limit for a call that does not request one.
func (b Bash) defaultTimeout() time.Duration {
	if b.Timeout <= 0 {
		return DefaultBashTimeout
	}
	return b.Timeout
}

// maxTimeout is the largest limit a call may ask for. A host that configures a
// longer default than the ceiling keeps it: clamping to MaxBashTimeout there
// would mean asking for more time yields less than saying nothing.
func (b Bash) maxTimeout() time.Duration {
	return max(MaxBashTimeout, b.defaultTimeout())
}

// timeoutFor resolves the model-supplied timeout. An unusable value is an error
// rather than a silent fallback to the default: a command that needs ten
// minutes would otherwise be killed at two and the model would never learn why.
func (b Bash) timeoutFor(input map[string]any) (time.Duration, error) {
	v, ok := input["timeout"]
	if !ok || v == nil {
		return b.defaultTimeout(), nil
	}
	var seconds float64
	switch n := v.(type) {
	case float64:
		seconds = n
	case int:
		seconds = float64(n)
	case int64:
		seconds = float64(n)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		// A number too large for a float64 is still a number, and ParseFloat
		// returns the saturated value with it; clamping it below beats
		// rejecting one spelling of "far too long" while accepting the rest.
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return 0, fmt.Errorf("input timeout must be a number of seconds, got %q", n)
		}
		seconds = parsed
	default:
		return 0, fmt.Errorf("input timeout must be a number of seconds, got %v", v)
	}
	if math.IsNaN(seconds) || seconds <= 0 {
		return 0, fmt.Errorf("input timeout must be a positive number of seconds, got %v", v)
	}
	// Clamp before converting: a duration is nanoseconds in an int64, so a
	// number of seconds beyond ~9.2e9 (or +Inf) overflows the conversion and
	// would come out negative, expiring the deadline before the command runs.
	ceiling := b.maxTimeout()
	if seconds >= ceiling.Seconds() {
		return ceiling, nil
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func (b Bash) Run(ctx context.Context, input map[string]any) (string, error) {
	cmd, err := mustString(input, "command")
	if err != nil {
		return "", err
	}
	timeout, err := b.timeoutFor(input)
	if err != nil {
		return "", err
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	start := time.Now()
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
			// The command's own deadline is not the only one: the agent's run
			// budget can expire first. Blaming the requested timeout then
			// tells the model a fast command was slow.
			if parent.Err() == context.DeadlineExceeded {
				return out, fmt.Errorf("bash command stopped after %s by the agent's deadline, before its %s timeout: %w", time.Since(start).Round(time.Second), timeout, ctxErr)
			}
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
