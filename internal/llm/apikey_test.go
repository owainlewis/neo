package llm

import (
	"strings"
	"testing"
)

func TestAPIKeyFromEnv_ReturnsConfiguredKey(t *testing.T) {
	t.Setenv("NEO_TEST_API_KEY", "configured-key")

	key, err := APIKeyFromEnv("NEO_TEST_API_KEY", "https://example.com/keys")
	if err != nil {
		t.Fatalf("APIKeyFromEnv: %v", err)
	}
	if key != "configured-key" {
		t.Fatalf("key = %q", key)
	}
}

func TestAPIKeyFromEnv_ExplainsHowToConfigureMissingKey(t *testing.T) {
	t.Setenv("NEO_TEST_API_KEY", " \t")

	_, err := APIKeyFromEnv("NEO_TEST_API_KEY", "https://example.com/keys")
	if err == nil {
		t.Fatal("expected missing key error")
	}
	for _, want := range []string{
		"NEO_TEST_API_KEY",
		"https://example.com/keys",
		"export NEO_TEST_API_KEY=\"your-api-key\"",
		"neo doctor",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}
