package notion

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// Notion allows roughly three requests per second per integration. A single
// token is shared between humans, CI jobs and the TUI, so transient 429s are
// expected rather than exceptional.
const (
	defaultMaxRetries = 4
	baseBackoff       = 500 * time.Millisecond
	maxBackoff        = 30 * time.Second
)

// WithMaxRetries caps how many times a retryable response is retried. Zero
// disables retrying; a negative value is ignored rather than honoured, because
// the retry loop would then run zero attempts and report success without ever
// reaching the network.
func WithMaxRetries(n int) Option {
	return func(c *Client) {
		if n >= 0 {
			c.maxRetries = n
		}
	}
}

// WithSleep replaces the sleep function. Tests use it to run instantly.
func WithSleep(f func(time.Duration)) Option { return func(c *Client) { c.sleep = f } }

// statusServiceOverload is Notion's 529. It has no net/http constant: the code
// is outside the registered range, and Notion documents it alongside 429 —
// "handling HTTP 429 and 529 responses and respecting the Retry-After response
// header value".
const statusServiceOverload = 529

// retryable reports whether a status code is worth another attempt.
func retryable(status int) bool {
	return status == http.StatusTooManyRequests ||
		status == statusServiceOverload ||
		status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout
}

// backoffFor returns how long to wait before attempt n (zero-based).
//
// Retry-After, when present, always wins: it is the server telling us exactly
// how long the bucket needs. Only the delay-seconds form is honoured; the
// HTTP-date form the RFC also allows falls through to the exponential backoff,
// which is a safe approximation.
func backoffFor(attempt int, header string) time.Duration {
	if header != "" {
		if secs, err := strconv.Atoi(header); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	d := baseBackoff << attempt
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

// wait sleeps through the client's seam while staying cancellable. The seam
// exists so tests do not actually wait; a real sleep races the context.
func (c *Client) wait(ctx context.Context, d time.Duration) error {
	done := make(chan struct{})
	go func() {
		c.sleep(d)
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}
