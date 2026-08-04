package notion

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetPageMarkdownParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("want GET, got %s", r.Method)
		}
		if r.URL.Path != "/v1/pages/page-1/markdown" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"page_markdown","id":"page-1",
			"markdown":"# Title\n\nBody.","truncated":false,
			"unknown_block_ids":[],"request_id":"req-1"}`))
	}))
	defer srv.Close()

	c := New("tok", WithBaseURL(srv.URL))
	got, err := c.GetPageMarkdown(context.Background(), "page-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Markdown != "# Title\n\nBody." {
		t.Errorf("markdown = %q", got.Markdown)
	}
	if got.Truncated {
		t.Error("truncated should be false")
	}
	if got.RequestID != "req-1" {
		t.Errorf("request_id = %q", got.RequestID)
	}
}

// A page with no content is normal, not an error: verified live (spec §10.f).
func TestGetPageMarkdownAcceptsEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"page_markdown","id":"p","markdown":"",
			"truncated":false,"unknown_block_ids":[]}`))
	}))
	defer srv.Close()

	got, err := New("tok", WithBaseURL(srv.URL)).GetPageMarkdown(context.Background(), "p")
	if err != nil {
		t.Fatalf("an empty body must not be an error: %v", err)
	}
	if got.Markdown != "" {
		t.Errorf("markdown = %q, want empty", got.Markdown)
	}
}

// truncated and unknown_block_ids must survive to the caller: a truncated page
// that looks complete is the failure this guards against.
func TestGetPageMarkdownSurfacesTruncationAndUnknownBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"page_markdown","id":"p","markdown":"partial",
			"truncated":true,"unknown_block_ids":["b1","b2"]}`))
	}))
	defer srv.Close()

	got, err := New("tok", WithBaseURL(srv.URL)).GetPageMarkdown(context.Background(), "p")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Truncated {
		t.Error("truncated must be reported")
	}
	if len(got.UnknownBlockIDs) != 2 {
		t.Errorf("unknown_block_ids = %v, want 2 entries", got.UnknownBlockIDs)
	}
}

func TestGetPageMarkdownNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"object":"error","status":404,"code":"object_not_found","message":"nope"}`))
	}))
	defer srv.Close()

	_, err := New("tok", WithBaseURL(srv.URL)).GetPageMarkdown(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestGetPageMarkdownUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"object":"error","status":401,"code":"unauthorized","message":"bad token"}`))
	}))
	defer srv.Close()

	_, err := New("tok", WithBaseURL(srv.URL)).GetPageMarkdown(context.Background(), "p")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestAppendPageMarkdownSendsInsertContentAtEnd(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("want PATCH, got %s", r.Method)
		}
		if r.URL.Path != "/v1/pages/p1/markdown" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		_, _ = w.Write([]byte(`{"object":"page_markdown","id":"p1",
			"markdown":"old\n## new","truncated":false,"unknown_block_ids":[]}`))
	}))
	defer srv.Close()

	got, err := New("tok", WithBaseURL(srv.URL)).
		AppendPageMarkdown(context.Background(), "p1", "## new")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["type"] != "insert_content" {
		t.Errorf(`type = %v, want "insert_content"`, gotBody["type"])
	}
	ic, ok := gotBody["insert_content"].(map[string]any)
	if !ok {
		t.Fatalf("insert_content missing or wrong type: %v", gotBody["insert_content"])
	}
	if ic["content"] != "## new" {
		t.Errorf("content = %v", ic["content"])
	}
	// position is sent explicitly even though omitting it also appends:
	// depending on an undocumented default is a free risk.
	pos, ok := ic["position"].(map[string]any)
	if !ok || pos["type"] != "end" {
		t.Errorf(`position = %v, want {"type":"end"}`, ic["position"])
	}
	// The PATCH returns the updated page, so the caller needs no second GET.
	if got.Markdown != "old\n## new" {
		t.Errorf("returned markdown = %q", got.Markdown)
	}
}

// The single most important guarantee here: an append is NOT idempotent, so an
// ambiguous outcome must never be retried automatically -- a blind retry
// duplicates content on the user's page.
func TestAppendPageMarkdownDoesNotRetryAmbiguousFailure(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"object":"error","status":500,"code":"internal_server_error","message":"boom"}`))
	}))
	defer srv.Close()

	_, err := New("tok", WithBaseURL(srv.URL)).
		AppendPageMarkdown(context.Background(), "p1", "x")
	if !errors.Is(err, ErrAmbiguousWrite) {
		t.Fatalf("want ErrAmbiguousWrite, got %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("a 500 must not be retried on a non-idempotent write; got %d calls", n)
	}
}

// 429 is different: Notion rejected the request without applying it, so
// retrying is safe and must happen.
func TestAppendPageMarkdownRetriesRateLimit(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"object":"error","status":429,"code":"rate_limited","message":"slow down"}`))
			return
		}
		_, _ = w.Write([]byte(`{"object":"page_markdown","id":"p1","markdown":"ok",
			"truncated":false,"unknown_block_ids":[]}`))
	}))
	defer srv.Close()

	// WithSleep like every other retry test in this package: without it the
	// backoff sleeps for real and the suite pays 500ms for nothing.
	got, err := New("tok", WithBaseURL(srv.URL), WithSleep(func(time.Duration) {})).
		AppendPageMarkdown(context.Background(), "p1", "x")
	if err != nil {
		t.Fatalf("a 429 must be retried to success: %v", err)
	}
	if got.Markdown != "ok" {
		t.Errorf("markdown = %q", got.Markdown)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("want 2 calls (429 then success), got %d", n)
	}
}
