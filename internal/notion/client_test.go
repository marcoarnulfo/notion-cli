package notion

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
