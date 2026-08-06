package notion

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// bigMarkdownPage returns a /markdown response whose JSON is at least n bytes,
// built from one long markdown string so the shape stays realistic.
func bigMarkdownPage(n int) []byte {
	body := strings.Repeat("word ", n/5+1)
	page, err := json.Marshal(map[string]any{
		"object": "page_markdown", "id": "p1", "markdown": body,
		"truncated": false, "unknown_block_ids": []string{},
	})
	if err != nil {
		panic(err)
	}
	return page
}

// A page larger than the default 1 MiB cap must still be readable: this
// endpoint is unpaginated, so that cap would otherwise be a hard ceiling on
// how large a page 'get --body' can read at all.
func TestGetPageMarkdownReadsPageOverDefaultCap(t *testing.T) {
	page := bigMarkdownPage(maxResponseBodyBytes + (1 << 16))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(page)
	}))
	defer srv.Close()

	got, err := New("tok", WithBaseURL(srv.URL)).GetPageMarkdown(context.Background(), "p1")
	if err != nil {
		t.Fatalf("a page over 1 MiB must still be readable: %v", err)
	}
	if len(got.Markdown) == 0 {
		t.Fatal("markdown came back empty")
	}
}

// The regression the reviewer found: the PATCH answers with the WHOLE updated
// page, so once a page passes the default cap the 200 was discarded for size
// and surfaced as a non-*APIError, which doRejectRetryable cannot tell from a
// transport failure -> ErrAmbiguousWrite on an append that actually landed.
// Permanent on append-only pages, and the advice it produced was the one that
// duplicates.
func TestAppendPageMarkdownSucceedsWhenResponseExceedsDefaultCap(t *testing.T) {
	page := bigMarkdownPage(maxResponseBodyBytes + (1 << 16))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(page)
	}))
	defer srv.Close()

	got, err := New("tok", WithBaseURL(srv.URL)).
		AppendPageMarkdown(context.Background(), "p1", "note")
	if err != nil {
		t.Fatalf("an append whose 200 exceeds the default cap must not be reported as failed: %v", err)
	}
	if errors.Is(err, ErrAmbiguousWrite) {
		t.Fatal("a successful append must never be reported as ambiguous")
	}
	if len(got.Markdown) == 0 {
		t.Fatal("markdown came back empty")
	}
}

// The larger ceiling is still a ceiling: past it the response is refused
// rather than decoded from a body we already know is cut off.
func TestMarkdownResponseStillCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Stream past the cap without allocating it all at once.
		chunk := strings.Repeat("x", 1<<20)
		for written := 0; written <= maxMarkdownResponseBytes; written += len(chunk) {
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	_, err := New("tok", WithBaseURL(srv.URL)).GetPageMarkdown(context.Background(), "p1")
	if err == nil {
		t.Fatal("a response past the markdown cap must be refused, not decoded")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("want a size error, got %v", err)
	}
}
