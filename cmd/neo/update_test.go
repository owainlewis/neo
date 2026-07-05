package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestInstallStableUpdateReplacesBinaryAfterChecksumVerification(t *testing.T) {
	const asset = "neo_darwin_amd64.tar.gz"
	archive := testTarGz(t, "neo", []byte("new binary\n"))
	checksum := testChecksumLine(asset, archive)
	srv := testUpdateServer(t, asset, archive, checksum)
	target := filepath.Join(t.TempDir(), "neo")
	if err := os.WriteFile(target, []byte("old binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := installStableUpdate(context.Background(), srv.Client(), srv.URL+"/releases", srv.URL+"/download", "v1.2.2", target, "darwin", "amd64")
	if err != nil {
		t.Fatalf("installStableUpdate: %v", err)
	}
	if got.Installed != "v1.2.2" || got.Latest != "v1.2.3" || got.AlreadyCurrent {
		t.Fatalf("result = %+v", got)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "new binary\n" {
		t.Fatalf("binary = %q, want new binary", string(body))
	}
}

func TestInstallStableUpdateDoesNotMutateWhenCurrent(t *testing.T) {
	downloads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"tag_name":"v1.2.3","prerelease":false}]`))
		default:
			downloads++
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	target := filepath.Join(t.TempDir(), "neo")
	if err := os.WriteFile(target, []byte("old binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := installStableUpdate(context.Background(), srv.Client(), srv.URL+"/releases", srv.URL+"/download", "v1.2.3", target, "darwin", "amd64")
	if err != nil {
		t.Fatalf("installStableUpdate: %v", err)
	}
	if !got.AlreadyCurrent {
		t.Fatalf("AlreadyCurrent = false, want true")
	}
	if downloads != 0 {
		t.Fatalf("download calls = %d, want 0", downloads)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "old binary\n" {
		t.Fatalf("binary = %q, want old binary", string(body))
	}
}

func TestInstallStableUpdateRejectsUnsupportedPlatform(t *testing.T) {
	if _, err := releaseAssetName("windows", "amd64"); err == nil || !strings.Contains(err.Error(), "unsupported platform") {
		t.Fatalf("releaseAssetName error = %v, want unsupported platform", err)
	}
	if _, err := releaseAssetName("darwin", "386"); err == nil || !strings.Contains(err.Error(), "unsupported platform") {
		t.Fatalf("releaseAssetName error = %v, want unsupported platform", err)
	}
}

func TestInstallStableUpdateChecksumMismatchLeavesOldBinary(t *testing.T) {
	const asset = "neo_darwin_amd64.tar.gz"
	archive := testTarGz(t, "neo", []byte("new binary\n"))
	srv := testUpdateServer(t, asset, archive, "0000  "+asset+"\n")
	target := filepath.Join(t.TempDir(), "neo")
	if err := os.WriteFile(target, []byte("old binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := installStableUpdate(context.Background(), srv.Client(), srv.URL+"/releases", srv.URL+"/download", "v1.2.2", target, "darwin", "amd64")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("installStableUpdate error = %v, want checksum mismatch", err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "old binary\n" {
		t.Fatalf("binary = %q, want old binary", string(body))
	}
}

func testUpdateServer(t *testing.T, asset string, archive []byte, checksums string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"tag_name":"v1.2.3","prerelease":false}]`))
		case "/download/v1.2.3/" + asset:
			_, _ = w.Write(archive)
		case "/download/v1.2.3/checksums.txt":
			_, _ = w.Write([]byte(checksums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testTarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testChecksumLine(asset string, body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x  %s\n", sum, asset)
}
