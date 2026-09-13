package llm

import (
	"strings"
	"testing"
)

func TestNewHTTPErrorParsesProviderShapes(t *testing.T) {
	cases := []struct {
		name, provider string
		status         int
		body, want     string
	}{
		{"anthropic", "anthropic", 401, `{"type":"error","error":{"type":"authentication_error","message":"API key is invalid."},"request_id":null}`,
			"anthropic 401 authentication_error: API key is invalid. (check ANTHROPIC_API_KEY)"},
		{"openai", "openai", 429, `{"error":{"message":"Rate limit reached","type":"rate_limit_error","code":"rate_limit_exceeded"}}`,
			"openai 429 rate_limit_error: Rate limit reached"},
		{"google", "google", 400, `{"error":{"code":400,"message":"API key not valid.","status":"INVALID_ARGUMENT"}}`,
			"google 400 INVALID_ARGUMENT: API key not valid."},
		{"openrouter", "openrouter", 401, `{"error":{"message":"Missing Authentication header","code":401}}`,
			"openrouter 401: Missing Authentication header (check OPENROUTER_API_KEY)"},
		{"codex", "openai-codex", 403, `{"error":{"message":"forbidden"}}`,
			"openai-codex 403: forbidden (run `neo login`)"},
		{"html", "anthropic", 502, "<html><body>Bad Gateway</body></html>",
			"anthropic 502: <html><body>Bad Gateway</body></html>"},
	}
	for _, tc := range cases {
		got := NewHTTPError(tc.provider, tc.status, []byte(tc.body)).Error()
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestNewHTTPErrorTrimsLongUnparseableBodies(t *testing.T) {
	got := NewHTTPError("openai", 500, []byte(strings.Repeat("x", 1000))).Error()
	if len(got) > 240 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long body not trimmed: %d chars", len(got))
	}
}
