package logx

import (
	"bytes"
	"encoding/json"
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

	var sink bytes.Buffer
	mu.Lock()
	logger = newLogger(&sink)
	mu.Unlock()

	if err := InitFromEnv(); err != nil {
		t.Fatalf("InitFromEnv: %v", err)
	}
	Debug("disabled-default", "value", "ignored")
	if sink.Len() != 0 {
		t.Fatalf("disabled logger wrote %q", sink.String())
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

func TestInitFromEnv_RedactsSupportedProviderSecretsEverywhere(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neo.log")
	providers := []struct {
		env      string
		sentinel string
	}{
		{env: "OPENAI_API_KEY", sentinel: "openai-provider-sentinel"},
		{env: "ANTHROPIC_API_KEY", sentinel: "anthropic-provider-sentinel"},
		{env: "OPENROUTER_API_KEY", sentinel: "openrouter-provider-sentinel"},
		{env: "GOOGLE_API_KEY", sentinel: "google-provider-sentinel"},
	}

	t.Setenv("NEO_LOG", path)
	t.Setenv("NEO_LOG_VERBOSE", "1")
	secrets := make([]string, 0, len(providers))
	for _, provider := range providers {
		t.Setenv(provider.env, provider.sentinel)
		secrets = append(secrets, provider.sentinel)
	}
	t.Cleanup(func() { _ = Close() })

	if err := InitFromEnv(); err != nil {
		t.Fatalf("InitFromEnv: %v", err)
	}

	safeText := SafeString("safe-string-content "+strings.Join(secrets, " "), 0)
	assertSecretsRedacted(t, "SafeString", safeText, secrets)
	if !strings.Contains(safeText, "safe-string-content") {
		t.Fatalf("SafeString removed ordinary content: %q", safeText)
	}

	safeValue := SafeAny(map[string]any{
		"outer": []any{
			map[string]any{
				"credentials": strings.Join(secrets, " "),
				"message":     "nested-safe-content",
			},
		},
	})
	safeJSON, err := json.Marshal(safeValue)
	if err != nil {
		t.Fatalf("marshal SafeAny result: %v", err)
	}
	assertSecretsRedacted(t, "nested SafeAny", string(safeJSON), secrets)
	if !strings.Contains(string(safeJSON), "nested-safe-content") {
		t.Fatalf("SafeAny removed ordinary content: %s", safeJSON)
	}

	payload := PayloadValue(`{"credentials":"` + strings.Join(secrets, " ") + `","message":"payload-safe-content"}`)
	assertSecretsRedacted(t, "verbose PayloadValue", payload.String(), secrets)
	if !strings.Contains(payload.String(), "payload-safe-content") {
		t.Fatalf("PayloadValue removed ordinary content: %q", payload.String())
	}

	Debug("provider secrets", "preview", safeText, "args", safeValue, "payload", payload)
	if err := Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	assertSecretsRedacted(t, "log output", text, secrets)
	for _, want := range []string{"safe-string-content", "nested-safe-content", "payload-safe-content", "[REDACTED]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("log output missing %q:\n%s", want, text)
		}
	}
}

func assertSecretsRedacted(t *testing.T, source string, value string, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(value, secret) {
			t.Fatalf("%s leaked %q: %s", source, secret, value)
		}
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
