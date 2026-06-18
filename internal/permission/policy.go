package permission

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/owainlewis/neo/internal/workspace"
)

type Decision int

const (
	Allow Decision = iota
	Ask
	Deny
)

type Request struct {
	ToolName string
	Args     map[string]any
}

type Result struct {
	Decision Decision
	Reason   string
}

type Policy interface {
	Decide(ctx context.Context, req Request) Result
}

type Mode string

const (
	ModeAsk      Mode = "ask"
	ModeTrusted  Mode = "trusted"
	ModeReadonly Mode = "readonly"
)

type WorkspacePolicy struct {
	Mode Mode
	Root string
}

func New(mode, root string) WorkspacePolicy {
	if mode == "" {
		mode = string(ModeAsk)
	}
	absRoot, err := filepath.Abs(root)
	if err == nil {
		root = absRoot
	}
	return WorkspacePolicy{Mode: Mode(mode), Root: filepath.Clean(root)}
}

func (p WorkspacePolicy) Decide(_ context.Context, req Request) Result {
	if p.Mode == "" {
		p.Mode = ModeAsk
	}
	if reason := p.pathDenial(req); reason != "" {
		return Result{Decision: Deny, Reason: reason}
	}
	switch p.Mode {
	case ModeReadonly:
		if isReadTool(req.ToolName) {
			return Result{Decision: Allow}
		}
		return Result{Decision: Deny, Reason: fmt.Sprintf("permission mode readonly denied %s", req.ToolName)}
	case ModeTrusted:
		return Result{Decision: Allow}
	default:
		if isReadTool(req.ToolName) {
			return Result{Decision: Allow}
		}
		return Result{Decision: Ask}
	}
}

func (p WorkspacePolicy) pathDenial(req Request) string {
	for _, key := range pathKeys(req.ToolName) {
		path, ok := stringArg(req.Args, key)
		if !ok || strings.TrimSpace(path) == "" {
			continue
		}
		if !p.contains(path) {
			return fmt.Sprintf("%s path %q is outside workspace root %s", req.ToolName, path, p.Root)
		}
	}
	return ""
}

func (p WorkspacePolicy) contains(path string) bool {
	_, err := workspace.ResolveWithin(p.Root, path)
	return err == nil
}

func pathKeys(tool string) []string {
	switch tool {
	case "read_file", "write_file", "edit_file":
		return []string{"path"}
	case "grep", "glob":
		return []string{"path"}
	default:
		return nil
	}
}

func isReadTool(tool string) bool {
	switch tool {
	case "read_file", "grep", "glob", "workflow":
		return true
	default:
		return false
	}
}

func stringArg(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
