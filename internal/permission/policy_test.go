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
