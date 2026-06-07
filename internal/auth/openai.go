package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ProviderOpenAICodex is the auth.json key under which ChatGPT/Codex
// subscription credentials are stored.
const ProviderOpenAICodex = "openai-codex"

// OpenAI device-code constants. The client ID and endpoints match the public
// Codex CLI flow; the subscription backend only accepts tokens minted this way.
const (
	openAIClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAIAuthBase = "https://auth.openai.com"

	// jwtAuthClaim is the namespaced claim in the access token that carries the
	// ChatGPT account id required by the subscription backend.
	jwtAuthClaim = "https://api.openai.com/auth"
)

// Endpoints are package vars (not consts) so tests can point the flow at a
// local httptest server and shorten the polling timeout.
var (
	openAIUserCodeURL        = openAIAuthBase + "/api/accounts/deviceauth/usercode"
	openAIDeviceTokenURL     = openAIAuthBase + "/api/accounts/deviceauth/token"
	openAIDeviceVerifyURL    = openAIAuthBase + "/codex/device"
	openAIDeviceRedirect     = openAIAuthBase + "/deviceauth/callback"
	openAITokenURL           = openAIAuthBase + "/oauth/token"
	defaultDevicePollTimeout = 15 * time.Minute
)

// Credentials is a stored token set for one provider.
type Credentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	AccountID    string    `json:"account_id"`
}

// Expired reports whether the access token is past its expiry, treating tokens
// within skew of expiry as already expired so callers refresh proactively.
func (c Credentials) Expired(skew time.Duration) bool {
	if c.ExpiresAt.IsZero() {
		return true
	}
	return time.Now().Add(skew).After(c.ExpiresAt)
}

// LoginOptions configures the interactive OpenAI login flow.
type LoginOptions struct {
	// OnDeviceCode is invoked once with the URL and code the user must enter in
	// a browser to authorize.
	OnDeviceCode func(verificationURL, userCode string)
	// HTTPClient overrides the client used for token exchange (tests).
	HTTPClient *http.Client
	// Timeout bounds the wait for device-code approval (default 15 minutes).
	Timeout time.Duration
}

// LoginOpenAI runs the Codex device-code flow against auth.openai.com and
// returns the resulting credentials. It blocks until the browser approval is
// completed, ctx is cancelled, or the timeout elapses.
func LoginOpenAI(ctx context.Context, opts LoginOptions) (Credentials, error) {
	httpc := httpClient(opts.HTTPClient)
	code, err := requestDeviceCode(ctx, httpc)
	if err != nil {
		return Credentials{}, err
	}
	if opts.OnDeviceCode != nil {
		opts.OnDeviceCode(code.VerificationURL, code.UserCode)
	}

	approved, err := pollDeviceCode(ctx, httpc, code, opts.Timeout)
	if err != nil {
		return Credentials{}, err
	}

	return exchangeCode(ctx, httpc, approved.AuthorizationCode, approved.CodeVerifier, openAIDeviceRedirect)
}

// RefreshOpenAI exchanges a refresh token for a fresh credential set.
func RefreshOpenAI(ctx context.Context, httpc *http.Client, refreshToken string) (Credentials, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {openAIClientID},
	}
	creds, err := postToken(ctx, httpClient(httpc), form)
	if err != nil {
		return Credentials{}, err
	}
	// A refresh response may omit a new refresh token; keep the existing one.
	if creds.RefreshToken == "" {
		creds.RefreshToken = refreshToken
	}
	return creds, nil
}

func exchangeCode(ctx context.Context, httpc *http.Client, code, verifier, redirectURI string) (Credentials, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {openAIClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	}
	return postToken(ctx, httpc, form)
}

type deviceCode struct {
	VerificationURL string
	UserCode        string
	DeviceAuthID    string
	Interval        time.Duration
}

type deviceToken struct {
	AuthorizationCode string
	CodeVerifier      string
}

func requestDeviceCode(ctx context.Context, httpc *http.Client) (deviceCode, error) {
	var out struct {
		DeviceAuthID string       `json:"device_auth_id"`
		UserCode     string       `json:"user_code"`
		Usercode     string       `json:"usercode"`
		Interval     pollInterval `json:"interval"`
	}
	if err := postJSON(ctx, httpc, openAIUserCodeURL, map[string]string{
		"client_id": openAIClientID,
	}, &out); err != nil {
		return deviceCode{}, err
	}
	userCode := out.UserCode
	if userCode == "" {
		userCode = out.Usercode
	}
	if out.DeviceAuthID == "" || userCode == "" {
		return deviceCode{}, fmt.Errorf("device-code response missing device_auth_id or user_code")
	}
	interval := time.Duration(out.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return deviceCode{
		VerificationURL: openAIDeviceVerifyURL,
		UserCode:        userCode,
		DeviceAuthID:    out.DeviceAuthID,
		Interval:        interval,
	}, nil
}

func pollDeviceCode(ctx context.Context, httpc *http.Client, code deviceCode, timeout time.Duration) (deviceToken, error) {
	if timeout <= 0 {
		timeout = defaultDevicePollTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		token, pending, err := pollDeviceToken(ctx, httpc, code)
		if err != nil {
			return deviceToken{}, err
		}
		if !pending {
			return token, nil
		}

		timer := time.NewTimer(code.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return deviceToken{}, fmt.Errorf("device-code login timed out after %s", timeout)
		case <-timer.C:
		}
	}
}

func pollDeviceToken(ctx context.Context, httpc *http.Client, code deviceCode) (deviceToken, bool, error) {
	var out struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	status, body, err := postJSONRaw(ctx, httpc, openAIDeviceTokenURL, map[string]string{
		"device_auth_id": code.DeviceAuthID,
		"user_code":      code.UserCode,
	})
	if err != nil {
		return deviceToken{}, false, err
	}
	if status == http.StatusForbidden || status == http.StatusNotFound {
		return deviceToken{}, true, nil
	}
	if status >= 400 {
		return deviceToken{}, false, fmt.Errorf("device-code token endpoint %d: %s", status, string(body))
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return deviceToken{}, false, fmt.Errorf("decode device-code token response: %w (body: %s)", err, string(body))
	}
	if out.AuthorizationCode == "" || out.CodeVerifier == "" {
		return deviceToken{}, false, fmt.Errorf("device-code token response missing authorization_code or code_verifier: %s", string(body))
	}
	return deviceToken{AuthorizationCode: out.AuthorizationCode, CodeVerifier: out.CodeVerifier}, false, nil
}

func postJSON(ctx context.Context, httpc *http.Client, endpoint string, in any, out any) error {
	status, body, err := postJSONRaw(ctx, httpc, endpoint, in)
	if err != nil {
		return err
	}
	if status >= 400 {
		return fmt.Errorf("device-code endpoint %d: %s", status, string(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode device-code response: %w (body: %s)", err, string(body))
	}
	return nil
}

func postJSONRaw(ctx context.Context, httpc *http.Client, endpoint string, in any) (int, []byte, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, nil
}

func postToken(ctx context.Context, httpc *http.Client, form url.Values) (Credentials, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", openAITokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpc.Do(req)
	if err != nil {
		return Credentials{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return Credentials{}, fmt.Errorf("openai token endpoint %d: %s", resp.StatusCode, string(body))
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return Credentials{}, fmt.Errorf("decode token response: %w (body: %s)", err, string(body))
	}
	if tr.AccessToken == "" {
		return Credentials{}, fmt.Errorf("token response missing access_token: %s", string(body))
	}

	accountID, err := accountIDFromToken(tr.AccessToken)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
		AccountID:    accountID,
	}, nil
}

// accountIDFromToken pulls chatgpt_account_id out of the access token's JWT
// payload. The subscription backend requires it as the chatgpt-account-id
// header.
func accountIDFromToken(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", fmt.Errorf("decode token payload: %w", err)
	}
	var claims struct {
		Auth struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parse token claims: %w", err)
	}
	if claims.Auth.ChatGPTAccountID == "" {
		return "", fmt.Errorf("token missing %s.chatgpt_account_id claim", jwtAuthClaim)
	}
	return claims.Auth.ChatGPTAccountID, nil
}

func httpClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: 30 * time.Second}
}

type pollInterval int

func (p *pollInterval) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		*p = pollInterval(n)
		return nil
	}

	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if strings.TrimSpace(s) == "" {
		*p = 0
		return nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return err
	}
	*p = pollInterval(parsed)
	return nil
}
