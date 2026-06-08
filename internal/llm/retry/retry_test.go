package retry

import (
	"net/http"
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
