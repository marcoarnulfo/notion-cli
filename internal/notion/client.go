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
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to the Notion API on behalf of an internal integration.
type Client struct {
	token   string
	baseURL string
	http    *http.Client
}

// Option customises a Client.
type Option func(*Client)

// WithBaseURL points the client at another host. Tests use it with httptest.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithHTTPClient replaces the underlying HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New builds a client authenticated with an integration token.
func New(token string, opts ...Option) *Client {
	c := &Client{
		token:   token,
		baseURL: BaseURL,
		http: &http.Client{
			Timeout: 30 * time.Second,
			// The stdlib only strips Authorization on redirect when the
			// hostname changes; it ignores port and scheme, so a same-host
			// redirect to another port, or an https->http downgrade, would
			// still carry the token along. The Notion API never redirects,
			// so refusing every redirect is strictly safer than trusting
			// that partial protection.
			CheckRedirect: refuseRedirects,
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// refuseRedirects rejects every redirect. It reports only the target URL,
// never headers, so the error itself cannot carry the Authorization token.
func refuseRedirects(req *http.Request, via []*http.Request) error {
	return fmt.Errorf("notion: refusing to follow redirect to %s", req.URL)
}

// do performs one request. body and out may be nil. Non-2xx responses are
// decoded into an *APIError.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
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

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("notion: reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &APIError{Status: resp.StatusCode, Code: "unknown", Message: string(raw)}
		var decoded struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &decoded) == nil && decoded.Code != "" {
			apiErr.Code, apiErr.Message = decoded.Code, decoded.Message
		}
		return apiErr
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("notion: decoding response: %w", err)
	}
	return nil
}
