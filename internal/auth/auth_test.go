package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// craftJWT builds a minimal unsigned JWT carrying the account-id claim.
func craftJWT(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(map[string]any{
		jwtAuthClaim: map[string]any{"chatgpt_account_id": accountID},
	})
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestGeneratePKCE_ChallengeMatchesVerifier(t *testing.T) {
	p, err := generatePKCE()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	sum := sha256.Sum256([]byte(p.Verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); p.Challenge != want {
		t.Fatalf("challenge mismatch: got %q want %q", p.Challenge, want)
	}
	if p.Verifier == "" || strings.ContainsAny(p.Verifier, "+/=") {
		t.Fatalf("verifier not base64url: %q", p.Verifier)
	}
}

func TestAccountIDFromToken(t *testing.T) {
	got, err := accountIDFromToken(craftJWT("acct_42"))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "acct_42" {
		t.Fatalf("account id: got %q want acct_42", got)
	}

	if _, err := accountIDFromToken("not-a-jwt"); err == nil {
		t.Fatal("expected error for non-JWT")
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	u := buildAuthorizeURL("chal", "st")
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("unparseable: %v", err)
	}
	q := parsed.Query()
	for k, want := range map[string]string{
		"response_type":         "code",
		"client_id":             openAIClientID,
		"code_challenge":        "chal",
		"code_challenge_method": "S256",
		"state":                 "st",
		"scope":                 openAIScope,
	} {
		if q.Get(k) != want {
			t.Errorf("param %s: got %q want %q", k, q.Get(k), want)
		}
	}
}

func TestExchangeAndRefresh(t *testing.T) {
	var lastForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		lastForm = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  craftJWT("acct_99"),
			"refresh_token": "refresh_2",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()

	old := openAITokenURL
	openAITokenURL = srv.URL
	defer func() { openAITokenURL = old }()

	creds, err := exchangeCode(context.Background(), srv.Client(), "the-code", "the-verifier")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if creds.AccountID != "acct_99" || creds.RefreshToken != "refresh_2" {
		t.Fatalf("bad creds: %+v", creds)
	}
	if lastForm.Get("grant_type") != "authorization_code" || lastForm.Get("code") != "the-code" {
		t.Fatalf("bad exchange form: %v", lastForm)
	}
	if creds.Expired(0) {
		t.Fatal("freshly issued token should not be expired")
	}

	// Refresh without a new refresh_token preserves the old one.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": craftJWT("acct_99"),
			"expires_in":   3600,
		})
	}))
	defer srv2.Close()
	openAITokenURL = srv2.URL

	refreshed, err := RefreshOpenAI(context.Background(), srv2.Client(), "refresh_old")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.RefreshToken != "refresh_old" {
		t.Fatalf("refresh token should be preserved, got %q", refreshed.RefreshToken)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store := NewStore(path)

	if _, ok, err := store.Get("missing"); err != nil || ok {
		t.Fatalf("expected absent, got ok=%v err=%v", ok, err)
	}

	creds := Credentials{AccessToken: "a", RefreshToken: "r", AccountID: "acct", ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.Set(ProviderOpenAICodex, creds); err != nil {
		t.Fatalf("set: %v", err)
	}

	// File must be 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("auth.json perms: got %o want 600", perm)
	}

	got, ok, err := store.Get(ProviderOpenAICodex)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.AccessToken != "a" || got.AccountID != "acct" {
		t.Fatalf("bad round-trip: %+v", got)
	}

	if err := store.Delete(ProviderOpenAICodex); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := store.Get(ProviderOpenAICodex); ok {
		t.Fatal("expected deleted")
	}
}

func TestTokenSource_RefreshesExpired(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	_ = store.Set(ProviderOpenAICodex, Credentials{
		AccessToken:  "old",
		RefreshToken: "refresh_1",
		ExpiresAt:    time.Now().Add(-time.Minute), // expired
		AccountID:    "acct",
	})

	ts := NewTokenSource(store, ProviderOpenAICodex)
	var refreshCalls int
	ts.refresh = func(_ context.Context, _ *http.Client, refreshToken string) (Credentials, error) {
		refreshCalls++
		if refreshToken != "refresh_1" {
			t.Fatalf("unexpected refresh token: %q", refreshToken)
		}
		return Credentials{AccessToken: "new", RefreshToken: "refresh_2", ExpiresAt: time.Now().Add(time.Hour), AccountID: "acct"}, nil
	}

	got, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if got.AccessToken != "new" || refreshCalls != 1 {
		t.Fatalf("expected one refresh to 'new', got %+v calls=%d", got, refreshCalls)
	}

	// Refreshed creds are persisted, and a second call uses them without
	// refreshing again.
	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatalf("token 2: %v", err)
	}
	if refreshCalls != 1 {
		t.Fatalf("expected no second refresh, got %d", refreshCalls)
	}
}

func TestTokenSource_NotLoggedIn(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	ts := NewTokenSource(store, ProviderOpenAICodex)
	if _, err := ts.Token(context.Background()); err == nil {
		t.Fatal("expected error when not logged in")
	}
}
