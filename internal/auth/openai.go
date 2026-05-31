package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProviderOpenAICodex is the auth.json key under which ChatGPT/Codex
// subscription credentials are stored.
const ProviderOpenAICodex = "openai-codex"

// OpenAI OAuth constants. The client ID and endpoints match the public Codex
// CLI flow; the subscription backend only accepts tokens minted this way.
const (
	openAIClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAIAuthBase = "https://auth.openai.com"
	openAIRedirect = "http://localhost:1455/auth/callback"
	openAIScope    = "openid profile email offline_access"
	callbackAddr   = "127.0.0.1:1455"

	// jwtAuthClaim is the namespaced claim in the access token that carries the
	// ChatGPT account id required by the subscription backend.
	jwtAuthClaim = "https://api.openai.com/auth"
)

// Endpoints are package vars (not consts) so tests can point the flow at a
// local httptest server.
var (
	openAIAuthorizeURL = openAIAuthBase + "/oauth/authorize"
	openAITokenURL     = openAIAuthBase + "/oauth/token"
)

// Credentials is a stored OAuth token set for one provider.
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
	// OnAuthURL is invoked once with the URL the user must visit to authorize.
	// Callers typically open it in a browser and also print it as a fallback.
	OnAuthURL func(url string)
	// HTTPClient overrides the client used for token exchange (tests).
	HTTPClient *http.Client
	// Timeout bounds the wait for the browser callback (default 5 minutes).
	Timeout time.Duration
}

// LoginOpenAI runs the authorization-code + PKCE flow against auth.openai.com,
// serving the redirect on a loopback port, and returns the resulting
// credentials. It blocks until the callback arrives, ctx is cancelled, or the
// timeout elapses.
func LoginOpenAI(ctx context.Context, opts LoginOptions) (Credentials, error) {
	p, err := generatePKCE()
	if err != nil {
		return Credentials{}, err
	}
	state, err := randomState()
	if err != nil {
		return Credentials{}, err
	}

	authURL := buildAuthorizeURL(p.Challenge, state)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv, err := startCallbackServer(state, codeCh, errCh)
	if err != nil {
		return Credentials{}, err
	}
	defer srv.Close()

	if opts.OnAuthURL != nil {
		opts.OnAuthURL(authURL)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return Credentials{}, err
	case <-ctx.Done():
		return Credentials{}, ctx.Err()
	case <-timer.C:
		return Credentials{}, fmt.Errorf("login timed out after %s", timeout)
	}

	return exchangeCode(ctx, httpClient(opts.HTTPClient), code, p.Verifier)
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

func buildAuthorizeURL(challenge, state string) string {
	q := url.Values{
		"response_type":              {"code"},
		"client_id":                  {openAIClientID},
		"redirect_uri":               {openAIRedirect},
		"scope":                      {openAIScope},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {"neo"},
	}
	return openAIAuthorizeURL + "?" + q.Encode()
}

func exchangeCode(ctx context.Context, httpc *http.Client, code, verifier string) (Credentials, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {openAIClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {openAIRedirect},
	}
	return postToken(ctx, httpc, form)
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

// callbackServer is the loopback HTTP server that captures the OAuth redirect.
type callbackServer struct {
	ln  net.Listener
	srv *http.Server
}

func (s *callbackServer) Close() { _ = s.srv.Close() }

func startCallbackServer(state string, codeCh chan<- string, errCh chan<- error) (*callbackServer, error) {
	ln, err := net.Listen("tcp", callbackAddr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s for OAuth callback: %w", callbackAddr, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			select {
			case errCh <- fmt.Errorf("oauth state mismatch"):
			default:
			}
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			select {
			case errCh <- fmt.Errorf("oauth callback missing code"):
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, callbackSuccessHTML)
		select {
		case codeCh <- code:
		default:
		}
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return &callbackServer{ln: ln, srv: srv}, nil
}

func httpClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: 30 * time.Second}
}

const callbackSuccessHTML = `<!doctype html><html><head><meta charset="utf-8"><title>neo</title></head>
<body style="font-family:system-ui;max-width:32rem;margin:5rem auto;text-align:center">
<h1>Login complete</h1><p>You can close this window and return to neo.</p></body></html>`
