package retry

import (
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const MaxDelay = 30 * time.Second

// RetryAfter carries a parsed Retry-After hint. Present distinguishes an
// explicit "retry now" hint from a missing or invalid header.
type RetryAfter struct {
	Delay   time.Duration
	Present bool
}

type JitterFunc func(max time.Duration) time.Duration

func Absent() RetryAfter {
	return RetryAfter{}
}

func Delay(base time.Duration, attempt int, retryAfter RetryAfter) time.Duration {
	return DelayWithJitter(base, attempt, retryAfter, randomJitter)
}

func DelayWithJitter(base time.Duration, attempt int, retryAfter RetryAfter, jitter JitterFunc) time.Duration {
	if retryAfter.Present {
		return capDelay(retryAfter.Delay)
	}
	return BackoffDelayWithJitter(base, attempt, jitter)
}

func BackoffDelayWithJitter(base time.Duration, attempt int, jitter JitterFunc) time.Duration {
	d := exponentialDelay(base, attempt)
	if jitter == nil || d >= MaxDelay {
		return d
	}
	span := d / 2
	if remaining := MaxDelay - d; span > remaining {
		span = remaining
	}
	if span <= 0 {
		return d
	}
	j := jitter(span)
	if j < 0 {
		j = 0
	}
	if j > span {
		j = span
	}
	return capDelay(d + j)
}

func ParseRetryAfterHeader(value string, now time.Time) RetryAfter {
	value = strings.TrimSpace(value)
	if value == "" {
		return Absent()
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			return Absent()
		}
		if seconds > int64(MaxDelay/time.Second) {
			return RetryAfter{Delay: MaxDelay, Present: true}
		}
		return RetryAfter{Delay: time.Duration(seconds) * time.Second, Present: true}
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return Absent()
	}
	delay := when.Sub(now)
	if delay < 0 {
		delay = 0
	}
	return RetryAfter{Delay: capDelay(delay), Present: true}
}

func exponentialDelay(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	if attempt < 0 {
		attempt = 0
	}
	d := base
	for range attempt {
		if d >= MaxDelay/2 {
			return MaxDelay
		}
		d *= 2
	}
	return capDelay(d)
}

func capDelay(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	if d > MaxDelay {
		return MaxDelay
	}
	return d
}

func randomJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(max) + 1))
}
