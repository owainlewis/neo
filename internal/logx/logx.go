package logx

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	defaultPreviewLimit = 240
	defaultPayloadLimit = 4096
)

var (
	bearerPattern    = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._-]+`)
	openAIKeyPattern = regexp.MustCompile(`sk-[A-Za-z0-9_-]+`)
	anthropicPattern = regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]+`)
	mu               sync.RWMutex
	logger           = newLogger(io.Discard)
	closer           io.Closer
	enabled          bool
	verbose          bool
	secrets          []string
)

func InitFromEnv() error {
	path := strings.TrimSpace(os.Getenv("NEO_LOG"))
	verboseEnabled := envBool("NEO_LOG_VERBOSE")
	secretValues := envSecrets()

	mu.Lock()
	defer mu.Unlock()

	if closer != nil {
		_ = closer.Close()
		closer = nil
	}

	verbose = verboseEnabled
	secrets = secretValues
	if path == "" {
		logger = newLogger(io.Discard)
		enabled = false
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logger = newLogger(io.Discard)
		enabled = false
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		logger = newLogger(io.Discard)
		enabled = false
		return err
	}
	logger = newLogger(f)
	closer = f
	enabled = true
	return nil
}

func Close() error {
	mu.Lock()
	defer mu.Unlock()

	logger = newLogger(io.Discard)
	enabled = false
	secrets = nil
	verbose = false
	if closer == nil {
		return nil
	}
	err := closer.Close()
	closer = nil
	return err
}

func Enabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return enabled
}

func Verbose() bool {
	mu.RLock()
	defer mu.RUnlock()
	return verbose
}

func Debug(msg string, args ...any) {
	mu.RLock()
	l := logger
	mu.RUnlock()
	l.Debug(msg, args...)
}

func SafeString(s string, limit int) string {
	if limit <= 0 {
		limit = defaultPreviewLimit
	}
	redacted := redactString(s)
	if len(redacted) <= limit {
		return redacted
	}
	return redacted[:limit] + "...(truncated)"
}

func SafeAny(v any) any {
	switch x := v.(type) {
	case string:
		return SafeString(x, defaultPreviewLimit)
	case []byte:
		return SafeString(string(x), defaultPreviewLimit)
	case []string:
		out := make([]string, len(x))
		for i, v := range x {
			out[i] = SafeString(v, defaultPreviewLimit)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			if sensitiveKey(k) {
				out[k] = "[REDACTED]"
				continue
			}
			out[k] = SafeAny(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = SafeAny(v)
		}
		return out
	default:
		return v
	}
}

func PayloadValue(payload string) slog.Value {
	payload = redactString(payload)
	if Verbose() {
		return slog.StringValue(payload)
	}
	sum := sha256.Sum256([]byte(payload))
	return slog.GroupValue(
		slog.Int("bytes", len(payload)),
		slog.String("sha256", hex.EncodeToString(sum[:])),
	)
}

func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func redactString(s string) string {
	mu.RLock()
	currentSecrets := append([]string(nil), secrets...)
	mu.RUnlock()
	for _, secret := range currentSecrets {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	s = bearerPattern.ReplaceAllString(s, "Bearer [REDACTED]")
	s = anthropicPattern.ReplaceAllString(s, "[REDACTED]")
	s = openAIKeyPattern.ReplaceAllString(s, "[REDACTED]")
	return s
}

func envSecrets() []string {
	names := []string{
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func envBool(name string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func sensitiveKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(k, "api_key") ||
		strings.Contains(k, "apikey") ||
		strings.Contains(k, "token") ||
		strings.Contains(k, "secret") ||
		strings.Contains(k, "password") ||
		strings.Contains(k, "authorization")
}
