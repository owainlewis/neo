package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/owainlewis/neo/internal/llm"
)

func newTestClient(srv *httptest.Server) *Client {
	return &Client{
		APIKey:     "test",
		Endpoint:   srv.URL,
		Version:    defaultVersion,
		HTTP:       srv.Client(),
		MaxRetries: 3,
		BaseDelay:  1 * time.Millisecond,
	}
}

func TestComplete_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test" {
			t.Errorf("missing api key header, got %q", got)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resp.StopReason != "end_turn" || len(resp.Content) != 1 || resp.Content[0].Text != "hi" {
		t.Fatalf("bad response: %+v", resp)
	}
}

func TestComplete_RetriesOn5xxThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(503)
			w.Write([]byte("overloaded"))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	resp, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
	if resp.Content[0].Text != "ok" {
		t.Fatalf("bad response: %+v", resp)
	}
}

func TestComplete_RetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(429)
			w.Write([]byte(`{"retry_after":0}`))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m"}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestComplete_DoesNotRetry4xxClientErrors(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"type":"invalid","message":"bad input"}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).Complete(context.Background(), llm.Request{Model: "m"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected 400 in error, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 attempt, got %d", got)
	}
}

func TestComplete_GivesUpAfterMaxRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.MaxRetries = 2
	_, err := c.Complete(context.Background(), llm.Request{Model: "m"})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&calls); got != 3 { // attempt 0 + 2 retries
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}
