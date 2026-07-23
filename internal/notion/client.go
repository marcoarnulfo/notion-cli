// Package notion is a minimal client for the Notion REST API.
//
// It speaks HTTP and nothing else: it knows no notion of "ticket" or "status".
// Only the endpoints notion-track actually needs are implemented, which is why
// there is no third-party SDK here — owning the client is what lets us control
// the Notion-Version header.
package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxResponseBodyBytes caps how much of any Notion response we buffer in
// memory. Real Notion payloads, including error bodies, are a few KB at
// most; this ceiling exists so a misbehaving proxy or WAF sitting in front
// of the API can't force us to hold megabytes of data just to produce one
// error line. 1 MiB is comfortably above any legitimate response while
// still bounding the worst case to a constant, small cost per request.
const maxResponseBodyBytes = 1 << 20 // 1 MiB

// maxErrorMessageBytes caps how much of a non-JSON error body is quoted
// verbatim in APIError.Message. It only needs to be long enough for a human
// to recognise what went wrong (e.g. the start of an HTML error page), not
// to reproduce the whole body.
const maxErrorMessageBytes = 2 << 10 // 2 KiB

// Client talks to the Notion API on behalf of an internal integration.
type Client struct {
	token      string
	baseURL    string
	http       *http.Client
	maxRetries int
	sleep      func(time.Duration)
}

// Option customises a Client.
type Option func(*Client)

// WithBaseURL points the client at another host. Tests use it with httptest.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithHTTPClient replaces the underlying HTTP client. This is the natural way
// to plug in a custom proxy or TLS config in production, so New still applies
// the redirect-refusal policy below to whatever client is supplied here.
//
// The extent of that guarantee is worth stating exactly, because the token is
// a shared secret and the policy only reaches one layer. New sets
// CheckRedirect when the supplied client leaves it nil; a caller-set
// CheckRedirect is left alone as an explicit choice, and its owner is then
// responsible for not letting the Authorization header follow a redirect.
// Neither case covers a custom Transport: a RoundTripper that resolves
// redirects on its own never surfaces a 3xx to http.Client, so CheckRedirect
// is never consulted and the token travels wherever that transport sends it.
// Supplying a Transport therefore means taking over this guarantee, not
// inheriting it.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New builds a client authenticated with an integration token.
func New(token string, opts ...Option) *Client {
	c := &Client{
		token:   token,
		baseURL: BaseURL,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
		maxRetries: defaultMaxRetries,
		sleep:      time.Sleep,
	}
	for _, o := range opts {
		o(c)
	}
	// Applied last, after every option (including a caller-supplied
	// WithHTTPClient) has run, so the policy can't be bypassed just by
	// passing a custom *http.Client — it depends only on the option order
	// otherwise. The stdlib only strips Authorization on redirect when the
	// hostname changes; it ignores port and scheme, so a same-host redirect
	// to another port, or an https->http downgrade, would still carry the
	// token along. The Notion API never redirects, so refusing every
	// redirect is strictly safer than trusting that partial protection. A
	// caller-set CheckRedirect is left alone: it is an explicit choice, and
	// forcibly overriding it would be surprising.
	if c.http.CheckRedirect == nil {
		c.http.CheckRedirect = refuseRedirects
	}
	return c
}

// refuseRedirects rejects every redirect. It reports only the target URL,
// never headers, so the error itself cannot carry the Authorization token.
func refuseRedirects(req *http.Request, via []*http.Request) error {
	return fmt.Errorf("notion: refusing to follow redirect to %s", req.URL)
}

// truncateMessage bounds a raw, non-JSON error body to maxErrorMessageBytes
// before it is quoted in APIError.Message, so an oversized body from a
// misbehaving intermediary can't blow up the size of an error we return.
func truncateMessage(raw []byte) string {
	if len(raw) <= maxErrorMessageBytes {
		return string(raw)
	}
	return string(raw[:maxErrorMessageBytes]) + "...(truncated)"
}

// do performs a request, retrying rate limits and transient server errors.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		err := c.doOnce(ctx, method, path, body, out)
		if err == nil {
			return nil
		}
		lastErr = err

		var apiErr *APIError
		if !errors.As(err, &apiErr) || !retryable(apiErr.Status) || attempt == c.maxRetries {
			return err
		}

		// Sleep through the seam so tests run instantly, but let a cancelled
		// context win: a 30s backoff must not outlive a Ctrl-C.
		if err := c.wait(ctx, backoffFor(attempt, apiErr.RetryAfter)); err != nil {
			return err
		}
	}
	return lastErr
}

// doOnce performs one request. body and out may be nil. Non-2xx responses are
// decoded into an *APIError.
func (c *Client) doOnce(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("notion: encoding request: %w", err)
		}
		payload = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return fmt.Errorf("notion: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Notion-Version", APIVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// url.Error would repeat the URL but never the header, so this is safe.
		return fmt.Errorf("notion: request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return fmt.Errorf("notion: reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &APIError{
			Status:     resp.StatusCode,
			Code:       "unknown",
			Message:    truncateMessage(raw),
			RetryAfter: resp.Header.Get("Retry-After"),
		}
		var decoded struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &decoded) == nil && decoded.Code != "" {
			// decoded.Message comes straight from the response body just
			// like the raw fallback above, so it needs the same cap: a
			// well-formed {code, message} body is the common shape of a
			// real Notion error and must not bypass maxErrorMessageBytes.
			apiErr.Code, apiErr.Message = decoded.Code, truncateMessage([]byte(decoded.Message))
		}
		return apiErr
	}

	// An empty body (e.g. a 204) is not a decoding failure: there is simply
	// nothing to decode, so out is left untouched instead of erroring on
	// json.Unmarshal's "unexpected end of JSON input".
	if out == nil || len(raw) == 0 {
		return nil
	}
	// io.LimitReader above is applied before this status-code check even
	// runs, so a legitimate 2xx body larger than the cap arrives here
	// truncated, and json.Unmarshal would fail with a generic "unexpected
	// end of JSON input" that misattributes the cause. len(raw) reaching the
	// cap exactly is what LimitReader produces whenever the real body was at
	// or beyond it, so treat that as "too large" rather than attempting to
	// decode a body we already know may be cut off. The one false positive
	// this accepts — a genuine response whose size lands on exactly the cap
	// — is an acceptable trade for not misreporting the far more likely
	// oversized case as a decoding bug.
	if len(raw) == maxResponseBodyBytes {
		return fmt.Errorf("notion: response exceeds maximum size of %d bytes", maxResponseBodyBytes)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("notion: decoding response: %w", err)
	}
	return nil
}
