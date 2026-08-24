package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/llm/chatcompletions"
)

func TestNewProviderCustom(t *testing.T) {
	t.Setenv("MY_ENDPOINT_KEY", "secret")
	cfg := &config.Config{Custom: config.CustomBackend{
		BaseURL:   "https://example.com/v1",
		APIKeyEnv: "MY_ENDPOINT_KEY",
	}}
	provider, err := newProvider(cfg, "custom")
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	if provider.Name() != "custom" {
		t.Fatalf("name = %q, want custom", provider.Name())
	}
	client, ok := provider.(*chatcompletions.Client)
	if !ok {
		t.Fatalf("provider type = %T, want *chatcompletions.Client", provider)
	}
	if client.Endpoint != "https://example.com/v1/chat/completions" {
		t.Fatalf("endpoint = %q", client.Endpoint)
	}
}

func TestProviderCredentialPresentCustom(t *testing.T) {
	cfg := &config.Config{Custom: config.CustomBackend{APIKeyEnv: "MY_ENDPOINT_KEY"}}
	t.Setenv("MY_ENDPOINT_KEY", "")
	if providerCredentialPresent(cfg, "custom") {
		t.Fatal("expected absent credential with unset env var")
	}
	t.Setenv("MY_ENDPOINT_KEY", "secret")
	if !providerCredentialPresent(cfg, "custom") {
		t.Fatal("expected present credential with set env var")
	}
	// A programmatically built config skips load-time normalization; the
	// default env var must still apply.
	t.Setenv("CUSTOM_API_KEY", "secret")
	if !providerCredentialPresent(&config.Config{}, "custom") {
		t.Fatal("expected present credential via default CUSTOM_API_KEY")
	}
}

func TestCustomModelChoicesFetchesFromEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.3"},{"id":"glm-4.6"}]}`))
	}))
	defer srv.Close()

	cfg := &config.Config{Model: "glm-5.3", Custom: config.CustomBackend{BaseURL: srv.URL + "/v1"}}
	var errOut bytes.Buffer
	choices := providerModelChoices(context.Background(), cfg, "custom", &errOut)
	if len(choices) != 2 {
		t.Fatalf("got %d choices, want 2: %#v", len(choices), choices)
	}
	if choices[0].ID != "glm-4.6" || choices[1].ID != "glm-5.3" {
		t.Fatalf("choices not sorted by id: %#v", choices)
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected warning: %q", errOut.String())
	}
}

func TestCustomModelChoicesFallsBackToConfiguredModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: "glm-5.3", Custom: config.CustomBackend{BaseURL: srv.URL}}
	var errOut bytes.Buffer
	choices := providerModelChoices(context.Background(), cfg, "custom", &errOut)
	if len(choices) != 1 || choices[0].ID != "glm-5.3" {
		t.Fatalf("choices = %#v, want configured model fallback", choices)
	}
	if !strings.Contains(errOut.String(), "warning") {
		t.Fatalf("expected warning on fetch failure, got %q", errOut.String())
	}
}

func TestDoctorCustomChecks(t *testing.T) {
	cfg := &config.Config{Provider: "custom", Model: "glm-5.3", Custom: config.CustomBackend{
		BaseURL:   "https://example.com/v1",
		APIKeyEnv: "MY_ENDPOINT_KEY",
	}}
	if c := doctorProviderCheck(cfg); c.Status != doctorPass {
		t.Fatalf("provider check = %#v, want pass", c)
	}
	t.Setenv("MY_ENDPOINT_KEY", "")
	if c := doctorCredentialCheck(cfg); c.Status != doctorFail || !strings.Contains(c.Detail, "MY_ENDPOINT_KEY") {
		t.Fatalf("credential check = %#v, want fail naming the env var", c)
	}
	t.Setenv("MY_ENDPOINT_KEY", "secret")
	if c := doctorCredentialCheck(cfg); c.Status != doctorPass {
		t.Fatalf("credential check = %#v, want pass", c)
	}
}
