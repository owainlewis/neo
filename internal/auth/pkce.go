// Package auth implements OAuth/OIDC login for OpenAI ChatGPT/Codex
// subscriptions and on-disk storage of the resulting credentials. It lets neo
// authenticate to OpenAI with a subscription instead of a pay-per-token API
// key. The OpenAI flow mirrors the public Codex CLI: authorization-code + PKCE
// against auth.openai.com, with credentials persisted to ~/.neo/auth.json.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// pkce holds a PKCE (RFC 7636) verifier/challenge pair.
type pkce struct {
	Verifier  string
	Challenge string
}

// generatePKCE produces a high-entropy verifier and its S256 challenge.
func generatePKCE() (pkce, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return pkce{}, fmt.Errorf("pkce: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return pkce{
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}, nil
}

// randomState returns a random opaque value for the OAuth `state` parameter.
func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("state: %w", err)
	}
	return hex.EncodeToString(b), nil
}
