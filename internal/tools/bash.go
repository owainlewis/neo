package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

type Bash struct {
	Timeout time.Duration
	CWD     string
}

func (Bash) Name() string { return "bash" }

func (Bash) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "bash",
		Description: "Run a shell command via /bin/bash -c. Returns combined stdout+stderr. Use for git, tests, builds, file inspection beyond Read.",
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
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	if err := c.Start(); err != nil {
		return buf.String(), err
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
		return out, fmt.Errorf("bash cancelled: %w", ctxErr)
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

func killProcessGroup(c *exec.Cmd) {
	if c.Process == nil {
		return
	}
	_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
}
