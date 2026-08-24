package custom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewRequiresAPIKey(t *testing.T) {
	t.Setenv("CUSTOM_API_KEY", "")
	t.Setenv("MY_ENDPOINT_KEY", "")
	for _, env := range []string{"", "MY_ENDPOINT_KEY"} {
		_, err := New("https://example.com/v1", env, "some-model")
		if err == nil {
			t.Fatalf("env %q: expected missing key error", env)
		}
		want := "MY_ENDPOINT_KEY"
		if env == "" {
			want = "CUSTOM_API_KEY"
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("env %q: error = %q, want it to name %q", env, err.Error(), want)
		}
	}
}

func TestNewJoinsChatCompletionsEndpoint(t *testing.T) {
	cases := []struct{ base, want string }{
		{"https://example.com/v1", "https://example.com/v1/chat/completions"},
		{"https://example.com/v1/", "https://example.com/v1/chat/completions"},
	}
	for _, tc := range cases {
		t.Setenv("CUSTOM_API_KEY", "secret")
		client, err := New(tc.base, "", "some-model")
		if err != nil {
			t.Fatalf("New(%q): %v", tc.base, err)
		}
		if client.Name() != "custom" {
			t.Fatalf("name = %q, want custom", client.Name())
		}
		if client.Endpoint != tc.want {
			t.Fatalf("endpoint = %q, want %q", client.Endpoint, tc.want)
		}
		if client.DefaultModel != "some-model" {
			t.Fatalf("default model = %q, want some-model", client.DefaultModel)
		}
		if client.APIKey != "secret" {
			t.Fatal("api key not read from env")
		}
	}
}

func TestModelsFetchesAuthenticatesAndSorts(t *testing.T) {
	t.Setenv("MY_ENDPOINT_KEY", "secret")
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"glm-5.3"},
			{"id":"glm-4.5","name":"GLM 4.5","description":"fast"},
			{"id":"","name":"skip me"}
		]}`))
	}))
	defer srv.Close()

	models, err := Models(context.Background(), srv.Client(), srv.URL+"/v1/", "MY_ENDPOINT_KEY")
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("authorization = %q, want bearer from named env var", gotAuth)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2 (blank id dropped): %#v", len(models), models)
	}
	if models[0].ID != "glm-4.5" || models[1].ID != "glm-5.3" {
		t.Fatalf("models not sorted by id: %#v", models)
	}
	if models[1].Name != "glm-5.3" {
		t.Fatalf("name = %q, want id fallback", models[1].Name)
	}
	if models[0].Name != "GLM 4.5" || models[0].Description != "fast" {
		t.Fatalf("model fields not mapped: %#v", models[0])
	}
}

func TestModelsErrorsOnNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	if _, err := Models(context.Background(), srv.Client(), srv.URL, ""); err == nil {
		t.Fatal("expected error on 401 status")
	}
}
