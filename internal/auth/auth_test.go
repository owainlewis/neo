package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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

	creds, err := exchangeCode(context.Background(), srv.Client(), "the-code", "the-verifier", "https://auth.openai.test/deviceauth/callback")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if creds.AccountID != "acct_99" || creds.RefreshToken != "refresh_2" {
		t.Fatalf("bad creds: %+v", creds)
	}
	if lastForm.Get("grant_type") != "authorization_code" || lastForm.Get("code") != "the-code" {
		t.Fatalf("bad exchange form: %v", lastForm)
	}
	if lastForm.Get("redirect_uri") != "https://auth.openai.test/deviceauth/callback" {
		t.Fatalf("bad redirect_uri: %v", lastForm)
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

func TestLoginOpenAI_DeviceCodeFlow(t *testing.T) {
	var sawUserCodeRequest, sawPoll, sawTokenExchange bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/usercode":
			sawUserCodeRequest = true
			var req map[string]string
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode usercode request: %v", err)
			}
			if req["client_id"] != openAIClientID {
				t.Fatalf("client_id: got %q want %q", req["client_id"], openAIClientID)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_auth_id": "device_1",
				"user_code":      "ABCD-1234",
				"interval":       "1",
			})
		case "/poll":
			sawPoll = true
			var req map[string]string
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode poll request: %v", err)
			}
			if req["device_auth_id"] != "device_1" || req["user_code"] != "ABCD-1234" {
				t.Fatalf("bad poll request: %v", req)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"authorization_code": "auth_code",
				"code_verifier":      "verifier_1",
			})
		case "/token":
			sawTokenExchange = true
			_ = r.ParseForm()
			if r.PostForm.Get("grant_type") != "authorization_code" ||
				r.PostForm.Get("code") != "auth_code" ||
				r.PostForm.Get("code_verifier") != "verifier_1" ||
				r.PostForm.Get("redirect_uri") != "https://auth.openai.test/deviceauth/callback" {
				t.Fatalf("bad token exchange form: %v", r.PostForm)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  craftJWT("acct_device"),
				"refresh_token": "refresh_device",
				"expires_in":    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	oldUserCodeURL := openAIUserCodeURL
	oldDeviceTokenURL := openAIDeviceTokenURL
	oldVerifyURL := openAIDeviceVerifyURL
	oldRedirect := openAIDeviceRedirect
	oldTokenURL := openAITokenURL
	openAIUserCodeURL = srv.URL + "/usercode"
	openAIDeviceTokenURL = srv.URL + "/poll"
	openAIDeviceVerifyURL = "https://auth.openai.test/codex/device"
	openAIDeviceRedirect = "https://auth.openai.test/deviceauth/callback"
	openAITokenURL = srv.URL + "/token"
	defer func() {
		openAIUserCodeURL = oldUserCodeURL
		openAIDeviceTokenURL = oldDeviceTokenURL
		openAIDeviceVerifyURL = oldVerifyURL
		openAIDeviceRedirect = oldRedirect
		openAITokenURL = oldTokenURL
	}()

	var gotURL, gotCode string
	creds, err := LoginOpenAI(context.Background(), LoginOptions{
		HTTPClient: srv.Client(),
		Timeout:    time.Second,
		OnDeviceCode: func(url, code string) {
			gotURL = url
			gotCode = code
		},
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if creds.AccountID != "acct_device" || creds.RefreshToken != "refresh_device" {
		t.Fatalf("bad creds: %+v", creds)
	}
	if gotURL != "https://auth.openai.test/codex/device" || gotCode != "ABCD-1234" {
		t.Fatalf("bad device prompt: url=%q code=%q", gotURL, gotCode)
	}
	if !sawUserCodeRequest || !sawPoll || !sawTokenExchange {
		t.Fatalf("missing request: usercode=%v poll=%v token=%v", sawUserCodeRequest, sawPoll, sawTokenExchange)
	}
}

func TestPollDeviceCode_WaitsWhilePending(t *testing.T) {
	var polls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls == 1 {
			http.Error(w, "pending", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_code": "auth_code",
			"code_verifier":      "verifier_1",
		})
	}))
	defer srv.Close()

	oldDeviceTokenURL := openAIDeviceTokenURL
	openAIDeviceTokenURL = srv.URL
	defer func() { openAIDeviceTokenURL = oldDeviceTokenURL }()

	got, err := pollDeviceCode(context.Background(), srv.Client(), deviceCode{
		DeviceAuthID: "device_1",
		UserCode:     "ABCD-1234",
		Interval:     time.Millisecond,
	}, time.Second)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if got.AuthorizationCode != "auth_code" || polls != 2 {
		t.Fatalf("bad poll result: got=%+v polls=%d", got, polls)
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
