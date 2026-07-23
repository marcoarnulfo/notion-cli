package notion

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDoSendsAuthAndVersionHeaders(t *testing.T) {
	var gotAuth, gotVersion, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("Notion-Version")
		gotContentType = r.Header.Get("Content-Type")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New("ntn_secret", WithBaseURL(srv.URL))
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.do(context.Background(), http.MethodPost, "/v1/things", map[string]string{"a": "b"}, &out); err != nil {
		t.Fatalf("do: %v", err)
	}

	if gotAuth != "Bearer ntn_secret" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotVersion != APIVersion {
		t.Errorf("Notion-Version = %q, want %q", gotVersion, APIVersion)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if !out.OK {
		t.Error("response was not decoded")
	}
}

func TestDoMapsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":"unauthorized","message":"API token is invalid."}`))
	}))
	defer srv.Close()

	err := New("bad", WithBaseURL(srv.URL)).do(context.Background(), http.MethodGet, "/v1/users/me", nil, nil)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
	// errors.Is only depends on Status; assert Code/Message too so a
	// regression in decoding either field from the response body is caught.
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %T, want *APIError", err)
	}
	if apiErr.Code != "unauthorized" {
		t.Errorf("Code = %q, want %q", apiErr.Code, "unauthorized")
	}
	if apiErr.Message != "API token is invalid." {
		t.Errorf("Message = %q, want %q", apiErr.Message, "API token is invalid.")
	}
}

func TestDoMapsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":"object_not_found","message":"Could not find data source."}`))
	}))
	defer srv.Close()

	err := New("t", WithBaseURL(srv.URL)).do(context.Background(), http.MethodGet, "/v1/data_sources/x", nil, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %T, want *APIError", err)
	}
	if apiErr.Code != "object_not_found" {
		t.Errorf("Code = %q, want %q", apiErr.Code, "object_not_found")
	}
	if apiErr.Message != "Could not find data source." {
		t.Errorf("Message = %q, want %q", apiErr.Message, "Could not find data source.")
	}
}

// The token is a shared secret: it must never surface in an error string.
func TestErrorsNeverLeakTheToken(t *testing.T) {
	const token = "ntn_supersecret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"code":"validation_error","message":"bad"}`))
	}))
	defer srv.Close()

	err := New(token, WithBaseURL(srv.URL)).do(context.Background(), http.MethodGet, "/v1/x", nil, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaked the token: %v", err)
	}
}

// net/http's built-in sensitive-header stripping on redirect compares only
// the hostname, not the port or scheme: a same-host redirect to a different
// port, or an https->http downgrade, would still carry Authorization along.
// The Notion API never redirects, so the client must refuse to follow any
// redirect rather than lean on that partial protection.
func TestDoRefusesToFollowRedirects(t *testing.T) {
	const token = "ntn_redirect_secret"

	var targetHit bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit = true
		w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/elsewhere", http.StatusFound)
	}))
	defer origin.Close()

	err := New(token, WithBaseURL(origin.URL)).do(context.Background(), http.MethodGet, "/v1/x", nil, nil)
	if err == nil {
		t.Fatal("expected an error when the server issues a redirect")
	}
	if targetHit {
		t.Fatal("client followed the redirect instead of refusing it")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("redirect error leaked the token: %v", err)
	}
}

// An oversized, non-JSON error body (e.g. a WAF or proxy error page) must
// not be buffered and echoed back verbatim: that would let a misbehaving
// intermediary balloon memory use and log size for every failed request.
func TestDoBoundsOversizedErrorBody(t *testing.T) {
	const hugeBodySize = 5 << 20 // 5 MiB, well above any real Notion payload.

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write(bytes.Repeat([]byte("x"), hugeBodySize))
	}))
	defer srv.Close()

	err := New("t", WithBaseURL(srv.URL)).do(context.Background(), http.MethodGet, "/v1/x", nil, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	const maxAcceptableErrorLen = 4096
	if got := len(err.Error()); got > maxAcceptableErrorLen {
		t.Fatalf("error message is %d bytes, want <= %d; oversized body was not bounded", got, maxAcceptableErrorLen)
	}
}

// A 2xx response with an empty body (e.g. a 204) is not a decoding failure:
// there is nothing to decode, so out must be left untouched instead of
// producing a confusing "unexpected end of JSON input" error.
func TestDoTreatsEmptySuccessBodyAsNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	out := struct {
		Sentinel string
	}{Sentinel: "untouched"}

	err := New("t", WithBaseURL(srv.URL)).do(context.Background(), http.MethodGet, "/v1/x", nil, &out)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if out.Sentinel != "untouched" {
		t.Fatalf("out was modified despite an empty response body: %+v", out)
	}
}

// c.http.Do fails with a *url.Error when the transport itself can't reach
// the server (as opposed to the server returning a non-2xx response). This
// is the path that guards the package's most important invariant, so it
// must be exercised explicitly: url.Error.Error() includes the request URL
// and verb but never headers, so the token must not leak through it either.
func TestDoTransportFailureNeverLeaksTheToken(t *testing.T) {
	const token = "ntn_transport_secret"

	// A server that is immediately closed leaves its address unreachable,
	// forcing http.Client.Do to fail at the transport level.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := srv.URL
	srv.Close()

	err := New(token, WithBaseURL(closedURL)).do(context.Background(), http.MethodGet, "/v1/x", nil, nil)
	if err == nil {
		t.Fatal("expected an error when the transport can't reach the server")
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Fatalf("got %T, want an error wrapping *url.Error", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("transport error leaked the token: %v", err)
	}
}
