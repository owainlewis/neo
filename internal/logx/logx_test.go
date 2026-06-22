package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitFromEnv_DisabledByDefault(t *testing.T) {
	t.Setenv("NEO_LOG", "")
	t.Setenv("NEO_LOG_VERBOSE", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Cleanup(func() { _ = Close() })

	if err := InitFromEnv(); err != nil {
		t.Fatalf("InitFromEnv: %v", err)
	}
	if Enabled() {
		t.Fatal("Enabled() = true, want false")
	}
	Debug("disabled-default", "value", "ignored")
	if entries, err := os.ReadDir(t.TempDir()); err != nil {
		t.Fatalf("ReadDir: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("unexpected files created while logging disabled: %v", entries)
	}
}

func TestInitFromEnv_WritesDebugLogsToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neo.log")
	t.Setenv("NEO_LOG", path)
	t.Setenv("NEO_LOG_VERBOSE", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Cleanup(func() { _ = Close() })

	if err := InitFromEnv(); err != nil {
		t.Fatalf("InitFromEnv: %v", err)
	}
	Debug("provider request", "provider", "openai", "payload", PayloadValue(`{"ok":true}`))
	if err := Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"msg":"provider request"`, `"provider":"openai"`, `"sha256":"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("log file missing %q:\n%s", want, text)
		}
	}
}

func TestInitFromEnv_RedactsSecretsAndSensitiveArgs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neo.log")
	t.Setenv("NEO_LOG", path)
	t.Setenv("NEO_LOG_VERBOSE", "1")
	t.Setenv("OPENAI_API_KEY", "sk-secret-123")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Cleanup(func() { _ = Close() })

	if err := InitFromEnv(); err != nil {
		t.Fatalf("InitFromEnv: %v", err)
	}
	Debug("tool call",
		"args", SafeAny(map[string]any{
			"api_key": "sk-secret-123",
			"text":    "Bearer sk-secret-123 should not survive; sk-ant-secret-456 should not survive either",
		}),
		"payload", PayloadValue(`{"api_key":"sk-secret-123","note":"Bearer sk-secret-123 and sk-ant-secret-456"}`),
	)
	if err := Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "sk-secret-123") {
		t.Fatalf("secret leaked into log:\n%s", text)
	}
	if strings.Contains(text, "sk-ant-secret-456") {
		t.Fatalf("anthropic-style secret leaked into log:\n%s", text)
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("expected redaction marker in log:\n%s", text)
	}
}

func TestPayloadValue_VerboseKeepsFullPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neo.log")
	payload := strings.Repeat("x", 5000)
	t.Setenv("NEO_LOG", path)
	t.Setenv("NEO_LOG_VERBOSE", "1")
	t.Cleanup(func() { _ = Close() })

	if err := InitFromEnv(); err != nil {
		t.Fatalf("InitFromEnv: %v", err)
	}
	Debug("verbose payload", "payload", PayloadValue(payload))
	if err := Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, payload) {
		t.Fatal("expected full payload in verbose logs")
	}
	if strings.Contains(text, "...(truncated)") {
		t.Fatalf("verbose payload should not be truncated:\n%s", text)
	}
}
