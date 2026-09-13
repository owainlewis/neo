package retry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseRetryAfterHeaderSeconds(t *testing.T) {
	hint := ParseRetryAfterHeader("2", time.Unix(0, 0))
	if !hint.Present {
		t.Fatal("expected Retry-After hint")
	}
	if hint.Delay != 2*time.Second {
		t.Fatalf("delay = %s, want 2s", hint.Delay)
	}
}

func TestParseRetryAfterHeaderHTTPDate(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	hint := ParseRetryAfterHeader(now.Add(3*time.Second).Format(http.TimeFormat), now)
	if !hint.Present {
		t.Fatal("expected Retry-After hint")
	}
	if hint.Delay != 3*time.Second {
		t.Fatalf("delay = %s, want 3s", hint.Delay)
	}
}

func TestDelayCapsRetryAfter(t *testing.T) {
	got := DelayWithJitter(500*time.Millisecond, 0, RetryAfter{Delay: time.Minute, Present: true}, func(time.Duration) time.Duration {
		t.Fatal("jitter should not be used when Retry-After is present")
		return 0
	})
	if got != MaxDelay {
		t.Fatalf("delay = %s, want %s", got, MaxDelay)
	}
}

func TestDelayHonorsZeroRetryAfter(t *testing.T) {
	got := DelayWithJitter(500*time.Millisecond, 0, RetryAfter{Delay: 0, Present: true}, func(time.Duration) time.Duration {
		t.Fatal("jitter should not be used when Retry-After is present")
		return 0
	})
	if got != 0 {
		t.Fatalf("delay = %s, want 0", got)
	}
}

func TestBackoffDelayAddsBoundedJitter(t *testing.T) {
	got := BackoffDelayWithJitter(2*time.Second, 1, func(max time.Duration) time.Duration {
		if max != 2*time.Second {
			t.Fatalf("jitter max = %s, want 2s", max)
		}
		return 1500 * time.Millisecond
	})
	if got != 5500*time.Millisecond {
		t.Fatalf("delay = %s, want 5.5s", got)
	}
}

func TestBackoffDelayJitterDoesNotExceedCap(t *testing.T) {
	got := BackoffDelayWithJitter(20*time.Second, 0, func(max time.Duration) time.Duration {
		if max != 10*time.Second {
			t.Fatalf("jitter max = %s, want 10s", max)
		}
		return max
	})
	if got != MaxDelay {
		t.Fatalf("delay = %s, want %s", got, MaxDelay)
	}
}

func TestSleepReturnsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := Sleep(ctx, time.Hour); err != context.Canceled {
		t.Fatalf("Sleep() error = %v, want context canceled", err)
	}
}

func TestDoDoesNotRetryPermanentErrors(t *testing.T) {
	attempts := 0
	_, err := Do(context.Background(), Options{Provider: "p", MaxRetries: 3, BaseDelay: time.Millisecond}, func(context.Context) (AttemptResult, error) {
		attempts++
		return AttemptResult{}, Permanent(errors.New("not logged in"))
	})
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if err == nil || err.Error() != "not logged in" {
		t.Fatalf("err = %v, want the unwrapped permanent error", err)
	}
}

func TestDoDoesNotRetryTimeouts(t *testing.T) {
	attempts := 0
	_, err := Do(context.Background(), Options{Provider: "p", MaxRetries: 3, BaseDelay: time.Millisecond}, func(context.Context) (AttemptResult, error) {
		attempts++
		return AttemptResult{}, ErrIdleTimeout
	})
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("err = %v, want idle timeout", err)
	}
}

func TestDoStillRetriesTransientTransportErrors(t *testing.T) {
	attempts := 0
	_, err := Do(context.Background(), Options{Provider: "p", MaxRetries: 2, BaseDelay: time.Millisecond}, func(context.Context) (AttemptResult, error) {
		attempts++
		if attempts < 3 {
			return AttemptResult{}, errors.New("connection reset")
		}
		return AttemptResult{Status: 200}, nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("attempts = %d err = %v, want 3 attempts and success", attempts, err)
	}
}

// A stalled body must fail with a timeout instead of blocking forever, and a
// body that keeps sending must not be cut off.
func TestIdleBodyCancelsStalledReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	body := IdleBody(pr, 50*time.Millisecond, cancel)
	defer body.Close()

	go func() {
		for i := 0; i < 3; i++ {
			time.Sleep(20 * time.Millisecond)
			_, _ = pw.Write([]byte("x"))
		}
		// Then stall: the guard should fire, cancel ctx, and we unblock the read.
		<-ctx.Done()
		_ = pw.CloseWithError(ctx.Err())
	}()

	buf := make([]byte, 16)
	got := 0
	var err error
	for {
		var n int
		n, err = body.Read(buf)
		got += n
		if err != nil {
			break
		}
	}
	if got != 3 {
		t.Fatalf("read %d bytes before the stall, want 3", got)
	}
	if !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("err = %v, want ErrIdleTimeout", err)
	}
	if ctx.Err() == nil {
		t.Fatal("idle guard did not cancel the request context")
	}
}

func TestIdleBodyClosePreventsLateCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := IdleBody(io.NopCloser(strings.NewReader("ok")), 10*time.Millisecond, cancel)
	if _, err := io.ReadAll(body); err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	time.Sleep(30 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("closed body still cancelled the context")
	}
}

func TestNewHTTPClientHasNoTotalTimeout(t *testing.T) {
	c := NewHTTPClient()
	if c.Timeout != 0 {
		t.Fatalf("Timeout = %s, want none", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.ResponseHeaderTimeout != ResponseHeaderTimeout {
		t.Fatalf("transport = %#v, want ResponseHeaderTimeout %s", c.Transport, ResponseHeaderTimeout)
	}
}
