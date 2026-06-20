package openrouter

import (
	"strings"
	"testing"
)

func TestNewRequiresAPIKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	_, err := New()
	if err == nil {
		t.Fatal("expected missing key error")
	}
	if !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestNewUsesOpenRouterDefaults(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "secret")
	client, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.Name() != "openrouter" {
		t.Fatalf("name = %q", client.Name())
	}
	if client.Endpoint != DefaultEndpoint {
		t.Fatalf("endpoint = %q", client.Endpoint)
	}
	if client.DefaultModel != DefaultModel {
		t.Fatalf("default model = %q", client.DefaultModel)
	}
}
