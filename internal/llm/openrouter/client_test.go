package openrouter

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestModelsFetchesAndSortsCatalogue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"z/model","name":"Z","description":"last"},
			{"id":"a/model","name":"A","description":"first"},
			{"id":"","name":"skip me"}
		]}`))
	}))
	defer srv.Close()

	orig := ModelsEndpoint
	ModelsEndpoint = srv.URL + "/api/v1/models"
	defer func() { ModelsEndpoint = orig }()

	models, err := Models(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2 (blank id dropped): %#v", len(models), models)
	}
	if models[0].ID != "a/model" || models[1].ID != "z/model" {
		t.Fatalf("models not sorted by id: %#v", models)
	}
	if models[0].Name != "A" || models[0].Description != "first" {
		t.Fatalf("model fields not mapped: %#v", models[0])
	}
}

func TestModelsErrorsOnNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	orig := ModelsEndpoint
	ModelsEndpoint = srv.URL
	defer func() { ModelsEndpoint = orig }()

	if _, err := Models(context.Background(), srv.Client()); err == nil {
		t.Fatal("expected error on 500 status")
	}
}
