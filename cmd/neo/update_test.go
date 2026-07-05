package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLatestStableReleaseSelectsHighestStableVTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"tag_name":"nightly-20260705","prerelease":true},
			{"tag_name":"v1.9.9","prerelease":false},
			{"tag_name":"v1.10.0","prerelease":false},
			{"tag_name":"v2.0.0-beta.1","prerelease":true},
			{"tag_name":"not-a-version","prerelease":false},
			{"tag_name":"v9.0.0","draft":true}
		]`))
	}))
	defer srv.Close()

	got, err := latestStableRelease(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("latestStableRelease: %v", err)
	}
	if got != "v1.10.0" {
		t.Fatalf("latest = %q, want v1.10.0", got)
	}
}

func TestLatestStableReleaseErrorsWhenMetadataMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"tag_name":"nightly-20260705","prerelease":true}]`))
	}))
	defer srv.Close()

	if _, err := latestStableRelease(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected error for missing stable release metadata")
	}
}

func TestLatestStableReleaseReportsHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := latestStableRelease(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected error for HTTP failure")
	}
}

func TestUpdateAvailableVersionComparison(t *testing.T) {
	tests := []struct {
		name      string
		installed string
		latest    string
		want      bool
	}{
		{"dev", "dev", "v1.2.3", true},
		{"empty dev", "", "v1.2.3", true},
		{"equal", "v1.2.3", "v1.2.3", false},
		{"dirty same base", "v1.2.3-dirty", "v1.2.3", false},
		{"older", "v1.2.2", "v1.2.3", true},
		{"newer", "v1.2.4", "v1.2.3", false},
		{"prerelease same base", "v1.2.3-beta.1", "v1.2.3", true},
		{"prerelease older base", "v1.2.2-beta.1", "v1.2.3", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := updateAvailable(tt.installed, tt.latest)
			if err != nil {
				t.Fatalf("updateAvailable: %v", err)
			}
			if got != tt.want {
				t.Fatalf("available = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUpdateAvailableRejectsUnsupportedInstalledVersion(t *testing.T) {
	if _, err := updateAvailable("build-from-source", "v1.2.3"); err == nil {
		t.Fatal("expected unsupported installed version error")
	}
}

func TestCheckStableUpdateUsesInjectedServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"tag_name":"v1.2.3","prerelease":false}]`))
	}))
	defer srv.Close()

	got, err := checkStableUpdate(context.Background(), srv.Client(), srv.URL, "v1.2.2")
	if err != nil {
		t.Fatalf("checkStableUpdate: %v", err)
	}
	if got.Installed != "v1.2.2" || got.Latest != "v1.2.3" || !got.Available {
		t.Fatalf("result = %+v", got)
	}
}
