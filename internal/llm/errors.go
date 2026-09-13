package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HTTPError is a provider's non-2xx response, reduced to what a user can act
// on. Every supported API wraps its failures as {"error":{...}} with a message
// and usually a type or status, so one parser covers them all. The raw body is
// kept for debug logs but never printed by Error().
type HTTPError struct {
	Provider string
	Status   int
	Type     string
	Message  string
	Body     []byte
}

// NewHTTPError builds an HTTPError from a response body. An unparseable body
// falls back to a trimmed excerpt so a proxy's HTML error page still says
// something without flooding the terminal.
func NewHTTPError(provider string, status int, body []byte) *HTTPError {
	e := &HTTPError{Provider: provider, Status: status, Body: body}
	var probe struct {
		Error struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &probe); err == nil && probe.Error.Message != "" {
		e.Message = probe.Error.Message
		e.Type = probe.Error.Type
		if e.Type == "" {
			e.Type = probe.Error.Status
		}
		if e.Type == "" {
			if code, ok := probe.Error.Code.(string); ok {
				e.Type = code
			}
		}
		return e
	}
	e.Message = excerpt(string(body), 200)
	return e
}

func (e *HTTPError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d", e.Provider, e.Status)
	if e.Type != "" {
		b.WriteString(" " + e.Type)
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if hint := e.hint(); hint != "" {
		b.WriteString(" (" + hint + ")")
	}
	return b.String()
}

// hint names the credential to check on an authentication failure, since the
// fix is almost always in the environment rather than in the request.
func (e *HTTPError) hint() string {
	if e.Status != 401 && e.Status != 403 {
		return ""
	}
	switch e.Provider {
	case "anthropic":
		return "check ANTHROPIC_API_KEY"
	case "openai":
		return "check OPENAI_API_KEY"
	case "openai-codex":
		return "run `neo login`"
	case "openrouter":
		return "check OPENROUTER_API_KEY"
	case "google":
		return "check GOOGLE_API_KEY"
	}
	return ""
}

// excerpt collapses whitespace and cuts at n runes, never mid-character.
func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
