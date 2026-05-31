package auth

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// refreshSkew is how far ahead of expiry a token is refreshed, so requests
// don't race the expiry boundary.
const refreshSkew = 5 * time.Minute

// TokenSource yields a valid OpenAI subscription access token, refreshing and
// persisting it through the Store when it nears expiry. It is safe for
// concurrent use.
type TokenSource struct {
	store *Store
	key   string
	httpc *http.Client
	// refresh is the refresh function; overridable in tests. Defaults to
	// RefreshOpenAI.
	refresh func(ctx context.Context, httpc *http.Client, refreshToken string) (Credentials, error)

	mu sync.Mutex
}

// NewTokenSource builds a TokenSource for the given provider key.
func NewTokenSource(store *Store, key string) *TokenSource {
	return &TokenSource{store: store, key: key, refresh: RefreshOpenAI}
}

// Token returns current, non-expired credentials, refreshing if needed. It
// returns an error if no credentials are stored (the user must log in).
func (ts *TokenSource) Token(ctx context.Context) (Credentials, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	creds, ok, err := ts.store.Get(ts.key)
	if err != nil {
		return Credentials{}, err
	}
	if !ok {
		return Credentials{}, fmt.Errorf("not logged in: run `neo login`")
	}
	if !creds.Expired(refreshSkew) {
		return creds, nil
	}
	if creds.RefreshToken == "" {
		return Credentials{}, fmt.Errorf("session expired and no refresh token; run `neo login`")
	}

	refreshed, err := ts.refresh(ctx, ts.httpc, creds.RefreshToken)
	if err != nil {
		return Credentials{}, fmt.Errorf("refresh token: %w", err)
	}
	if err := ts.store.Set(ts.key, refreshed); err != nil {
		return Credentials{}, fmt.Errorf("persist refreshed token: %w", err)
	}
	return refreshed, nil
}
