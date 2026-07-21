package permission

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
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
	ReadOnly bool
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
		mode = string(ModeTrusted)
	}
	absRoot, err := filepath.Abs(root)
	if err == nil {
		root = absRoot
	}
	return WorkspacePolicy{Mode: Mode(mode), Root: filepath.Clean(root)}
}

func (p WorkspacePolicy) Decide(_ context.Context, req Request) Result {
	if p.Mode == "" {
		p.Mode = ModeTrusted
	}
	if reason := p.pathDenial(req); reason != "" {
		return Result{Decision: Deny, Reason: reason}
	}
	if reason := p.explicitApprovalReason(req); reason != "" {
		switch p.Mode {
		case ModeReadonly:
			// Readonly denies mutating tools outright below.
		default:
			return Result{Decision: Ask, Reason: reason}
		}
	}
	switch p.Mode {
	case ModeReadonly:
		if req.ReadOnly || isReadTool(req.ToolName) {
			return Result{Decision: Allow}
		}
		return Result{Decision: Deny, Reason: fmt.Sprintf("permission mode readonly denied %s", req.ToolName)}
	case ModeTrusted:
		return Result{Decision: Allow}
	default:
		if req.ReadOnly || isReadTool(req.ToolName) {
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

func explicitApprovalReason(req Request) string {
	return (WorkspacePolicy{Root: "."}).explicitApprovalReason(req)
}

func (p WorkspacePolicy) explicitApprovalReason(req Request) string {
	if req.ToolName != "bash" {
		return ""
	}
	cmd, ok := stringArg(req.Args, "command")
	if !ok {
		return ""
	}
	if reason := dangerousBashReason(cmd); reason != "" {
		return reason
	}
	if reason := p.bashPathApprovalReason(cmd); reason != "" {
		return reason
	}
	return ""
}

func (p WorkspacePolicy) bashPathApprovalReason(cmd string) string {
	for _, field := range bashPathCandidates(cmd) {
		if strings.HasPrefix(field, "~") {
			return "bash path outside workspace requires approval"
		}
		if filepath.IsAbs(field) || field == ".." || strings.HasPrefix(field, "../") || strings.Contains(field, "/../") {
			if !p.contains(field) {
				return "bash path outside workspace requires approval"
			}
		}
	}
	return ""
}

var bashPathCandidatePattern = regexp.MustCompile("(?:^|[\\s<>=])['\"]?((?:~|/|\\.\\./)[^\\s;|&'\"`]+)")

func bashPathCandidates(cmd string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.Trim(s, "'\" ")
		s = strings.TrimLeft(s, "<>")
		for i := 0; i < len(s); i++ {
			if s[i] == '=' {
				s = s[i+1:]
			}
		}
		if s == "" || seen[s] {
			return
		}
		if strings.HasPrefix(s, "~") || filepath.IsAbs(s) || s == ".." || strings.HasPrefix(s, "../") || strings.Contains(s, "/../") {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, segment := range shellSegments(cmd) {
		for _, field := range shellFields(segment) {
			add(field)
		}
	}
	for _, m := range bashPathCandidatePattern.FindAllStringSubmatch(cmd, -1) {
		if len(m) > 1 {
			add(m[1])
		}
	}
	return out
}

func dangerousBashReason(cmd string) string {
	for _, segment := range shellSegments(cmd) {
		fields := shellFields(segment)
		if len(fields) == 0 {
			continue
		}
		fields = skipEnvAssignments(fields)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "sudo":
			return "sudo command requires approval"
		case "rm":
			if rmRecursiveForce(fields[1:]) {
				return "recursive forced removal requires approval"
			}
		case "chmod", "chown", "chgrp":
			if hasRecursiveFlag(fields[1:]) {
				return fields[0] + " recursive change requires approval"
			}
		case "git":
			if dangerousGit(fields[1:]) {
				return "destructive git command requires approval"
			}
		}
	}
	return ""
}

func shellSegments(cmd string) []string {
	return strings.FieldsFunc(cmd, func(r rune) bool {
		switch r {
		case ';', '&', '|', '\n':
			return true
		default:
			return false
		}
	})
}

func shellFields(segment string) []string {
	fields := strings.Fields(segment)
	for i, f := range fields {
		fields[i] = strings.Trim(f, "'\"")
	}
	return fields
}

func skipEnvAssignments(fields []string) []string {
	for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "-") {
		fields = fields[1:]
	}
	return fields
}

func rmRecursiveForce(args []string) bool {
	recursive := false
	force := false
	for _, arg := range args {
		switch arg {
		case "--recursive":
			recursive = true
		case "--force":
			force = true
		default:
			if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
				recursive = recursive || strings.ContainsAny(arg, "rR")
				force = force || strings.Contains(arg, "f")
			}
		}
	}
	return recursive && force
}

func hasRecursiveFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--recursive" || arg == "-R" {
			return true
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "R") {
			return true
		}
	}
	return false
}

func dangerousGit(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "reset" {
		for _, arg := range args[1:] {
			if arg == "--hard" {
				return true
			}
		}
	}
	if args[0] == "clean" {
		force := false
		dirs := false
		dryRun := false
		for _, arg := range args[1:] {
			if arg == "--dry-run" || arg == "-n" {
				dryRun = true
			}
			if arg == "--force" {
				force = true
			}
			if arg == "-d" {
				dirs = true
			}
			if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
				force = force || strings.Contains(arg, "f")
				dirs = dirs || strings.Contains(arg, "d")
				dryRun = dryRun || strings.Contains(arg, "n")
			}
		}
		return force && dirs && !dryRun
	}
	return false
}

func stringArg(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
