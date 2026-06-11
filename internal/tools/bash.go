package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
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

	c := exec.CommandContext(ctx, "/bin/bash", "-c", cmd)
	if b.CWD != "" {
		c.Dir = b.CWD
	}
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	runErr := c.Run()
	out := buf.String()
	if runErr != nil {
		// Surface as an error so the agent marks the tool_result with is_error=true.
		// Keep the captured output in the message so the model can see what happened.
		if ctx.Err() != nil {
			return out, fmt.Errorf("bash cancelled: %w", ctx.Err())
		}
		if ee, ok := runErr.(*exec.ExitError); ok {
			return out, fmt.Errorf("exit %d", ee.ExitCode())
		}
		return out, runErr
	}
	return out, nil
}
