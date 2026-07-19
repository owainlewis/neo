package retry

import (
	"context"
	"fmt"
	"time"

	"github.com/owainlewis/neo/internal/logx"
)

const defaultBaseDelay = 500 * time.Millisecond

type AttemptResult struct {
	Body       []byte
	Status     int
	RetryAfter RetryAfter
}

type AttemptFunc func(context.Context) (AttemptResult, error)

type RetryAfterBodyFunc func([]byte) RetryAfter

type Options struct {
	Provider       string
	MaxRetries     int
	BaseDelay      time.Duration
	Retryable      func(status int) bool
	RetryAfterBody RetryAfterBodyFunc
	ErrorLabel     string
}

func Do(ctx context.Context, opts Options, attemptFn AttemptFunc) (AttemptResult, error) {
	maxRetries := opts.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	baseDelay := opts.BaseDelay
	if baseDelay <= 0 {
		baseDelay = defaultBaseDelay
	}
	retryable := opts.Retryable
	if retryable == nil {
		retryable = DefaultRetryableStatus
	}
	label := opts.ErrorLabel
	if label == "" {
		label = opts.Provider
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		logx.Debug("provider attempt", "provider", opts.Provider, "attempt", attempt+1, "max_attempts", maxRetries+1)
		result, err := attemptFn(ctx)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				logx.Debug("provider request canceled", "provider", opts.Provider, "error", ctx.Err().Error())
				return AttemptResult{}, ctx.Err()
			}
			if attempt == maxRetries {
				logx.Debug("provider transport failed", "provider", opts.Provider, "attempt", attempt+1, "error", err.Error())
				return AttemptResult{}, err
			}
			delay := Delay(baseDelay, attempt, Absent())
			logx.Debug("provider retry scheduled",
				"provider", opts.Provider,
				"attempt", attempt+1,
				"reason", "transport_error",
				"delay", delay.String(),
				"error", err.Error(),
			)
			if err := Sleep(ctx, delay); err != nil {
				return AttemptResult{}, err
			}
			continue
		}

		if retryable(result.Status) {
			lastErr = fmt.Errorf("%s %d: %s", label, result.Status, string(result.Body))
			if attempt == maxRetries {
				logx.Debug("provider retries exhausted",
					"provider", opts.Provider,
					"status", result.Status,
					"body", logx.PayloadValue(string(result.Body)),
				)
				return AttemptResult{}, lastErr
			}
			retryAfter := result.RetryAfter
			if !retryAfter.Present && opts.RetryAfterBody != nil {
				retryAfter = opts.RetryAfterBody(result.Body)
			}
			delay := Delay(baseDelay, attempt, retryAfter)
			logx.Debug("provider retry scheduled",
				"provider", opts.Provider,
				"attempt", attempt+1,
				"reason", "http_retryable",
				"status", result.Status,
				"delay", delay.String(),
				"retry_after_present", retryAfter.Present,
				"body", logx.PayloadValue(string(result.Body)),
			)
			if err := Sleep(ctx, delay); err != nil {
				return AttemptResult{}, err
			}
			continue
		}
		return result, nil
	}
	return AttemptResult{}, lastErr
}

func DefaultRetryableStatus(status int) bool {
	return status == 429 || status >= 500
}

func Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
