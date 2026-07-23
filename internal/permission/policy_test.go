package permission

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspacePolicyModes(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "file.txt")
	outside := filepath.Join(t.TempDir(), "file.txt")

	tests := []struct {
		name string
		mode string
		req  Request
		want Decision
	}{
		{"ask allows read", "ask", Request{ToolName: "read_file", Args: map[string]any{"path": inside}}, Allow},
		{"ask asks bash", "ask", Request{ToolName: "bash", Args: map[string]any{"command": "go test ./..."}}, Ask},
		{"ask asks write", "ask", Request{ToolName: "write_file", Args: map[string]any{"path": inside}}, Ask},
		{"trusted allows bash", "trusted", Request{ToolName: "bash", Args: map[string]any{"command": "go test ./..."}}, Allow},
		{"trusted allows rm rf", "trusted", Request{ToolName: "bash", Args: map[string]any{"command": "rm -rf build"}}, Allow},
		{"trusted allows sudo", "trusted", Request{ToolName: "bash", Args: map[string]any{"command": "sudo make install"}}, Allow},
		{"trusted allows write", "trusted", Request{ToolName: "write_file", Args: map[string]any{"path": inside}}, Allow},
		{"readonly denies bash", "readonly", Request{ToolName: "bash", Args: map[string]any{"command": "date"}}, Deny},
		{"readonly denies write", "readonly", Request{ToolName: "write_file", Args: map[string]any{"path": inside}}, Deny},
		{"outside path denied", "trusted", Request{ToolName: "read_file", Args: map[string]any{"path": outside}}, Deny},
		{"outside write denied", "trusted", Request{ToolName: "write_file", Args: map[string]any{"path": outside}}, Deny},
		{"outside edit denied", "trusted", Request{ToolName: "edit_file", Args: map[string]any{"path": outside}}, Deny},
		{"outside grep denied", "trusted", Request{ToolName: "grep", Args: map[string]any{"path": outside}}, Deny},
		{"outside glob denied", "trusted", Request{ToolName: "glob", Args: map[string]any{"path": outside}}, Deny},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(tt.mode, root).Decide(context.Background(), tt.req)
			if got.Decision != tt.want {
				t.Fatalf("decision = %v, want %v (reason %q)", got.Decision, tt.want, got.Reason)
			}
		})
	}
}

func TestWorkspacePolicyAllowsRuntimeClassifiedReadOnlyCall(t *testing.T) {
	policy := New(string(ModeReadonly), t.TempDir())
	allowed := policy.Decide(context.Background(), Request{ToolName: "agent", ReadOnly: true})
	if allowed.Decision != Allow {
		t.Fatalf("read-only capability decision=%+v", allowed)
	}
	denied := policy.Decide(context.Background(), Request{ToolName: "agent"})
	if denied.Decision != Deny {
		t.Fatalf("unclassified agent decision=%+v", denied)
	}
}

func TestWorkspacePolicyDangerousBashAllowedInTrustedMode(t *testing.T) {
	root := t.TempDir()
	tests := []string{
		"rm -rf build",
		"rm -fr build",
		"rm -r -f build",
		"rm --recursive --force build",
		"echo ok && rm -rf build",
		"sudo make install",
		"chmod -R 777 .",
		"chown -R user .",
		"chgrp --recursive staff .",
		"git clean -fd",
		"git reset --hard HEAD~1",
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			got := New("trusted", root).Decide(context.Background(), Request{
				ToolName: "bash",
				Args:     map[string]any{"command": cmd},
			})
			if got.Decision != Allow {
				t.Fatalf("decision = %v, want Allow (reason %q)", got.Decision, got.Reason)
			}
		})
	}
}

func TestWorkspacePolicyDangerousBashRequiresApprovalInAskMode(t *testing.T) {
	root := t.TempDir()
	tests := []string{
		"rm -rf build",
		"sudo make install",
		"git reset --hard HEAD~1",
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			got := New("ask", root).Decide(context.Background(), Request{
				ToolName: "bash",
				Args:     map[string]any{"command": cmd},
			})
			if got.Decision != Ask {
				t.Fatalf("decision = %v, want Ask (reason %q)", got.Decision, got.Reason)
			}
			if got.Reason == "" {
				t.Fatal("expected reason for dangerous command approval")
			}
		})
	}
}

func TestWorkspacePolicyTrustedAllowsRoutineBash(t *testing.T) {
	root := t.TempDir()
	tests := []string{
		"go test ./...",
		"git status --short",
		"rm build.log",
		"git clean -nfd",
	}
	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			got := New("trusted", root).Decide(context.Background(), Request{
				ToolName: "bash",
				Args:     map[string]any{"command": cmd},
			})
			if got.Decision != Allow {
				t.Fatalf("decision = %v, want Allow (reason %q)", got.Decision, got.Reason)
			}
		})
	}
}

func TestAllowlistDoesNotBypassDangerousBashApproval(t *testing.T) {
	var allow Allowlist
	req := Request{ToolName: "bash", Args: map[string]any{"command": "rm -rf build"}}
	allow.Add(RuleFor(req))
	if allow.Allows(req) {
		t.Fatal("dangerous bash commands must still require explicit approval")
	}
}
func TestWorkspacePolicyEmptyModeDefaultsToTrusted(t *testing.T) {
	got := New("", t.TempDir()).Decide(context.Background(), Request{
		ToolName: "bash",
		Args:     map[string]any{"command": "go test ./..."},
	})
	if got.Decision != Allow {
		t.Fatalf("decision = %v, want Allow (reason %q)", got.Decision, got.Reason)
	}
}

func TestWorkspacePolicyRelativePathUsesProcessCWD(t *testing.T) {
	root := t.TempDir()
	old, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	got := New("ask", root).Decide(context.Background(), Request{
		ToolName: "read_file",
		Args:     map[string]any{"path": "README.md"},
	})
	if got.Decision != Allow {
		t.Fatalf("decision = %v, reason %q", got.Decision, got.Reason)
	}
}

func TestWorkspacePolicyDeniesSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	got := New("trusted", root).Decide(context.Background(), Request{
		ToolName: "read_file",
		Args:     map[string]any{"path": filepath.Join(root, "link", "secret.txt")},
	})
	if got.Decision != Deny {
		t.Fatalf("decision = %v, want Deny", got.Decision)
	}
}

func TestWorkspacePolicyDeniesMutationSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	tests := []Request{
		{ToolName: "write_file", Args: map[string]any{"path": filepath.Join(root, "link", "new.txt")}},
		{ToolName: "edit_file", Args: map[string]any{"path": filepath.Join(root, "link", "secret.txt")}},
	}

	for _, req := range tests {
		t.Run(req.ToolName, func(t *testing.T) {
			got := New("trusted", root).Decide(context.Background(), req)
			if got.Decision != Deny {
				t.Fatalf("decision = %v, want Deny", got.Decision)
			}
		})
	}
}

func TestWorkspacePolicyBashOutsideWorkspaceAllowedInTrustedMode(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "README.md")
	if err := os.WriteFile(inside, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []string{
		"cat " + inside,
		"cat /etc/passwd",
		"cat ~/.ssh/id_rsa",
		"cat ../secret.txt",
		"cat </etc/passwd",
		"cat</etc/passwd",
		"echo hi 2>/tmp/neo.err",
		"grep --file=/etc/passwd needle",
	}

	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			got := New("trusted", root).Decide(context.Background(), Request{
				ToolName: "bash",
				Args:     map[string]any{"command": cmd},
			})
			if got.Decision != Allow {
				t.Fatalf("decision = %v, want Allow (reason %q)", got.Decision, got.Reason)
			}
		})
	}
}

func TestWorkspacePolicyBashOutsideWorkspaceRequiresApprovalInAskMode(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "README.md")
	if err := os.WriteFile(inside, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		cmd  string
		want Decision
	}{
		{"absolute inside allowed", "cat " + inside, Ask},
		{"absolute outside asks", "cat /etc/passwd", Ask},
		{"home path asks", "cat ~/.ssh/id_rsa", Ask},
		{"parent escape asks", "cat ../secret.txt", Ask},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New("ask", root).Decide(context.Background(), Request{
				ToolName: "bash",
				Args:     map[string]any{"command": tt.cmd},
			})
			if got.Decision != tt.want {
				t.Fatalf("decision = %v, want %v (reason %q)", got.Decision, tt.want, got.Reason)
			}
		})
	}
}
