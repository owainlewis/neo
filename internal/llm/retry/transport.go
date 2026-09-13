package retry

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Transport deadlines. Neo never sets http.Client.Timeout: a total deadline
// caps how long one generation may take, which is exactly the cliff a long
// reasoning turn falls off. Instead the connection has to keep showing signs
// of life. Headers must arrive within ResponseHeaderTimeout (a non-streaming
// API sends them only once generation finishes, so this is generous), and once
// the body starts each read must make progress within IdleTimeout.
const (
	ResponseHeaderTimeout = 10 * time.Minute
	IdleTimeout           = 3 * time.Minute
)

// ErrIdleTimeout is returned by an idle-guarded body when the peer sent nothing
// for IdleTimeout. It reports as a timeout so Do does not retry it.
var ErrIdleTimeout = idleTimeoutError{}

type idleTimeoutError struct{}

func (idleTimeoutError) Error() string {
	return "no data received from provider for " + IdleTimeout.String()
}
func (idleTimeoutError) Timeout() bool   { return true }
func (idleTimeoutError) Temporary() bool { return false }

// NewHTTPClient returns a client with connection-level deadlines and no total
// timeout. See the constants above for why.
func NewHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = ResponseHeaderTimeout
	return &http.Client{Transport: transport}
}

// IdleBody wraps a response body so a stall longer than idle cancels the
// request. cancel must cancel the context the request was made with; the
// transport then fails the pending read, which the wrapper reports as
// ErrIdleTimeout. Close stops the timer.
func IdleBody(body io.ReadCloser, idle time.Duration, cancel context.CancelFunc) io.ReadCloser {
	b := &idleBody{ReadCloser: body, idle: idle, cancel: cancel}
	b.timer = time.AfterFunc(idle, b.expire)
	return b
}

type idleBody struct {
	io.ReadCloser
	idle   time.Duration
	cancel context.CancelFunc
	timer  *time.Timer

	mu       sync.Mutex
	timedOut bool
	closed   bool
}

func (b *idleBody) expire() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.timedOut = true
	b.mu.Unlock()
	b.cancel()
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	if b.timedOut {
		b.mu.Unlock()
		if err != nil {
			return n, ErrIdleTimeout
		}
		return n, nil
	}
	if !b.closed {
		b.timer.Reset(b.idle)
	}
	b.mu.Unlock()
	return n, err
}

func (b *idleBody) Close() error {
	b.mu.Lock()
	b.closed = true
	b.timer.Stop()
	b.mu.Unlock()
	return b.ReadCloser.Close()
}

// ReadAllIdle reads a body to completion under the idle guard. ctx is the
// request's context; the returned cancel must already be deferred by the
// caller, since cancelling it here would release the connection early.
func ReadAllIdle(body io.ReadCloser, cancel context.CancelFunc) ([]byte, error) {
	guarded := IdleBody(body, IdleTimeout, cancel)
	defer guarded.Close()
	return io.ReadAll(guarded)
}

// Permanent marks an error that retrying cannot fix: a missing login, a
// malformed response body, a request the client itself rejected. Do returns
// it after the first attempt instead of spending the retry budget on it.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// retryableTransportError reports whether a failed attempt is worth
// repeating. Timeouts are not: the request already ran for the full deadline,
// and repeating a generation that long compounds the wait several times over.
func retryableTransportError(err error) bool {
	var perm permanentError
	if errors.As(err, &perm) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return false
	}
	return true
}

// unwrapPermanent strips the marker so callers see the underlying error.
func unwrapPermanent(err error) error {
	var perm permanentError
	if errors.As(err, &perm) {
		return perm.err
	}
	return err
}
