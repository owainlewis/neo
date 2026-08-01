package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDispatchesHelpAndVersionToInjectedOutput(t *testing.T) {
	oldVersion := Version
	Version = "v1.2.3-test"
	t.Cleanup(func() { Version = oldVersion })

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "help", args: []string{"help"}, want: "USAGE:"},
		{name: "version", args: []string{"version"}, want: "neo version v1.2.3-test\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			streams, out, errOut := bufferedStdio()

			if code := run(tt.args, streams); code != 0 {
				t.Fatalf("run(%q) code = %d, want 0", tt.args, code)
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Fatalf("stdout = %q, want text containing %q", out.String(), tt.want)
			}
			if errOut.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", errOut.String())
			}
		})
	}
}

func TestRunUnknownCommandReturnsUsageError(t *testing.T) {
	streams, out, errOut := bufferedStdio()

	if code := run([]string{"wat"}, streams); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "unknown command: wat") {
		t.Fatalf("stderr = %q, want unknown-command error", errOut.String())
	}
	if !strings.Contains(out.String(), "USAGE:") {
		t.Fatalf("stdout = %q, want usage", out.String())
	}
}

func TestRunReportsConfigurationFailure(t *testing.T) {
	root := brokenConfigWorkspace(t)
	t.Chdir(root)
	streams, _, errOut := bufferedStdio()

	if code := run([]string{"chat"}, streams); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "config:") {
		t.Fatalf("stderr = %q, want config error", errOut.String())
	}
}

func TestRunHeadlessReportsArgumentAndProviderFailures(t *testing.T) {
	t.Run("missing prompt", func(t *testing.T) {
		streams, _, errOut := bufferedStdio()

		if code := run([]string{"run", "--json"}, streams); code != 2 {
			t.Fatalf("code = %d, want 2", code)
		}
		if !strings.Contains(errOut.String(), "neo run: missing prompt") {
			t.Fatalf("stderr = %q, want missing-prompt error", errOut.String())
		}
	})

	t.Run("provider", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("HOME", t.TempDir())
		t.Chdir(root)
		if err := os.WriteFile(filepath.Join(root, "neo.yaml"), []byte("provider: invalid\nmodel: test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		streams, _, errOut := bufferedStdio()
		streams.in = strings.NewReader("hello")

		if code := run([]string{"run"}, streams); code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		if !strings.Contains(errOut.String(), `unknown provider "invalid"`) {
			t.Fatalf("stderr = %q, want provider error", errOut.String())
		}
	})
}

func TestRunDoctorReturnsFailureStatusThroughDispatcher(t *testing.T) {
	root := brokenConfigWorkspace(t)
	t.Chdir(root)
	streams, out, errOut := bufferedStdio()

	if code := run([]string{"doctor"}, streams); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(out.String(), "fail") || !strings.Contains(out.String(), "config") {
		t.Fatalf("stdout = %q, want failed config check", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestRunRejectsInvalidSessionInput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	for _, tt := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{name: "missing resume id", args: []string{"resume"}, code: 2, want: "usage: neo resume <session-id>"},
		{name: "malformed resume id", args: []string{"resume", "../bad"}, code: 1, want: "invalid session id"},
		{name: "missing search query", args: []string{"sessions", "search"}, code: 2, want: "usage: neo sessions search <query>"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			streams, _, errOut := bufferedStdio()

			if code := run(tt.args, streams); code != tt.code {
				t.Fatalf("code = %d, want %d", code, tt.code)
			}
			if !strings.Contains(errOut.String(), tt.want) {
				t.Fatalf("stderr = %q, want text containing %q", errOut.String(), tt.want)
			}
		})
	}
}

func TestExecuteRunsCleanupBeforeReturningFailure(t *testing.T) {
	streams, _, _ := bufferedStdio()
	var events []string

	code := execute([]string{"wat"}, streams, lifecycle{
		init: func() error {
			events = append(events, "init")
			return nil
		},
		close: func() error {
			events = append(events, "close")
			return nil
		},
	})

	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if got := strings.Join(events, ","); got != "init,close" {
		t.Fatalf("lifecycle = %q, want init,close", got)
	}
}

func bufferedStdio() (stdio, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return stdio{in: strings.NewReader(""), out: out, err: errOut}, out, errOut
}

func brokenConfigWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "neo.yaml"), []byte("permissions: [invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}
