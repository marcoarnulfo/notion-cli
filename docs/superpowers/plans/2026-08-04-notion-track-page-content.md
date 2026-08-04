# notion-track — Page content (`get --body`, `--append-file`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `notion-track` read a page body back as Markdown (`get --body`) and append to one without destroying what is already there (`--append-file`), closing the read/write asymmetry the tool ships with today.

**Architecture:** Two new methods on the existing `notion.Client` wrap Notion's native Markdown API (`GET /v1/pages/{id}/markdown`, `PATCH .../markdown` with `insert_content`). `internal/service` gains a read path and an append branch in `BodyRequest`; `internal/cli` adds flags to `get`, `upsert` and `set`. Everything is **additive**: the existing `--body-file` replace path (blocks + `O(n)` deletes) is not touched, so no current behaviour can regress.

**Tech Stack:** Go 1.26, cobra, standard library `net/http` + `httptest` for tests. No new dependencies.

Spec: `docs/superpowers/specs/2026-08-04-notion-track-page-content-design.md` (API already verified live — spec §10).

## Global Constraints

- **Notion API version**: `2026-03-11`, already set in `internal/notion/version.go`. Do not change it.
- **No new third-party dependencies.** `go.mod` must be unchanged by this work.
- **Repo language**: code, comments, tests, README and SKILL in **English**. Only the design/plan docs are in Italian.
- **`internal/markdown` must not be touched.** Reading is rendered by Notion; appending sends raw Markdown. `ValidateAppendable` governs the *blocks* path only and must not be applied to the append path.
- **`--body-file` behaviour must not change.** No edits to `replaceBody`, `AppendBlockChildren`, `DeleteBlock` or their tests.
- **`pageJSON` keys are public API** (`internal/cli/get.go:11`). Existing keys must keep their exact names and types; new output goes under a new top-level key.
- **stdout = the answer, stderr = diagnostics.** Warnings go through `printWarnings` to `cmd.ErrOrStderr()`.
- **Retry discipline**: GET uses `c.do`; the non-idempotent PATCH uses `c.doRejectRetryable` (retries only 429/503/529, marks 500/502/504 and transport errors `ErrAmbiguousWrite`). Never retry the append on an ambiguous outcome.
- Run `gofmt -w` on every file you touch. `go vet ./...` must pass before each commit.
- **One expected failure spans Tasks 4–8.** `TestEveryAgentFacingFlagIsDocumented`
  (`internal/cli/skilldoc_test.go:69`) derives the flag list from the binary and requires
  SKILL.md to document every flag, so it fails from the moment `--body` exists until Task 10
  documents it. That is the harness working. **Do not** silence it by editing the test or by
  documenting the flags early — Task 10 is where it closes. Every other test must be green
  at every commit.

---

### Task 1: `notion.PageMarkdown` + `GetPageMarkdown` (read)

**Files:**
- Create: `internal/notion/markdown.go`
- Test: `internal/notion/markdown_test.go`

**Interfaces:**
- Consumes: `Client.do` (`internal/notion/client.go:112`), `ErrNotFound` / `ErrUnauthorized` (`internal/notion/errors.go`), `notion.New` + `WithBaseURL`.
- Produces:
  ```go
  type PageMarkdown struct {
      Markdown        string
      Truncated       bool
      UnknownBlockIDs []string
      RequestID       string
  }
  func (c *Client) GetPageMarkdown(ctx context.Context, pageID string) (PageMarkdown, error)
  ```

Response shape confirmed live (spec §10): `{object, id, markdown, truncated, unknown_block_ids, request_id}`.

- [ ] **Step 1: Write the failing tests**

Create `internal/notion/markdown_test.go`:

```go
package notion

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
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
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/notion/ -run TestGetPageMarkdown -v`
Expected: FAIL — compile error, `c.GetPageMarkdown` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/notion/markdown.go`:

```go
package notion

import (
	"context"
	"net/http"
	"net/url"
)

// PageMarkdown is the response of both GET and PATCH /v1/pages/{id}/markdown:
// the two share one shape, so appending returns the resulting page rather than
// a bare acknowledgement.
//
// Truncated and UnknownBlockIDs are carried all the way to the caller on
// purpose. Notion truncates around 20,000 blocks and renders unsupported types
// (bookmark, embed, link preview, breadcrumb, template button) as
// <unknown .../>; a reader who is not told would believe they hold the whole
// page.
type PageMarkdown struct {
	Markdown        string
	Truncated       bool
	UnknownBlockIDs []string
	// RequestID is the id Notion support asks for when diagnosing a call. It
	// costs nothing to keep and is worth quoting in an error message.
	RequestID string
}

// pageMarkdownResponse is the wire shape, kept separate so the exported type
// carries Go names rather than JSON ones.
type pageMarkdownResponse struct {
	Markdown        string   `json:"markdown"`
	Truncated       bool     `json:"truncated"`
	UnknownBlockIDs []string `json:"unknown_block_ids"`
	RequestID       string   `json:"request_id"`
}

func (r pageMarkdownResponse) toPageMarkdown() PageMarkdown {
	return PageMarkdown{
		Markdown:        r.Markdown,
		Truncated:       r.Truncated,
		UnknownBlockIDs: r.UnknownBlockIDs,
		RequestID:       r.RequestID,
	}
}

// GetPageMarkdown returns the page body rendered as Markdown by Notion, in one
// call: no recursion into child blocks, and block types this tool cannot build
// itself (tables, callouts, toggles) still read back correctly.
//
// GET is idempotent, so it uses do and gets the full retry policy.
func (c *Client) GetPageMarkdown(ctx context.Context, pageID string) (PageMarkdown, error) {
	var resp pageMarkdownResponse
	path := "/v1/pages/" + url.PathEscape(pageID) + "/markdown"
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return PageMarkdown{}, err
	}
	return resp.toPageMarkdown(), nil
}
```

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/notion/ -run TestGetPageMarkdown -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Format, vet, full suite**

Run: `gofmt -w internal/notion/markdown.go internal/notion/markdown_test.go && go vet ./... && go test ./...`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/notion/markdown.go internal/notion/markdown_test.go
git commit -m "feat(notion): read a page body back as Markdown

GET /v1/pages/{id}/markdown returns the whole page in one call, so
reading needs no recursion and block types this tool cannot build --
tables, callouts, toggles -- still read back correctly.

Truncated and UnknownBlockIDs are carried to the caller rather than
dropped: a page truncated at Notion's block ceiling would otherwise look
complete to whoever reads it."
```

---

### Task 2: `AppendPageMarkdown` (non-destructive append)

**Files:**
- Modify: `internal/notion/markdown.go`
- Test: `internal/notion/markdown_test.go`

**Interfaces:**
- Consumes: `PageMarkdown`, `pageMarkdownResponse` (Task 1); `Client.doRejectRetryable` (`internal/notion/blocks.go`), `ErrAmbiguousWrite`.
- Produces: `func (c *Client) AppendPageMarkdown(ctx context.Context, pageID, content string) (PageMarkdown, error)`

Verified live (spec §10): `insert_content` works on `2026-03-11`, emits no deprecation headers, preserves existing content, and returns the full updated page.

- [ ] **Step 1: Write the failing tests**

Append to `internal/notion/markdown_test.go`:

```go
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
```

Add `"encoding/json"`, `"sync/atomic"` and `"time"` to the test file's imports.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/notion/ -run TestAppendPageMarkdown -v`
Expected: FAIL — `c.AppendPageMarkdown` undefined.

- [ ] **Step 3: Write the implementation**

Append to `internal/notion/markdown.go`:

```go
// AppendPageMarkdown adds content to the END of a page, leaving everything
// already there untouched. It is the non-destructive counterpart to
// AppendBlockChildren + DeleteBlock, which together own the whole body.
//
// It returns the resulting page: the PATCH answers with the full updated
// Markdown rather than an acknowledgement, so a caller that gets a response
// knows exactly what the page now holds, with no follow-up GET.
//
// PATCH here is NOT idempotent -- running it twice appends twice -- so it uses
// doRejectRetryable, which retries only the statuses where Notion certainly
// refused the request (429/503/529) and joins anything ambiguous with
// ErrAmbiguousWrite instead of guessing.
//
// content must be non-empty: Notion answers 200 and does nothing at all for an
// empty string (verified, spec §10.e), so an empty append would look like a
// success. Callers validate before reaching here.
func (c *Client) AppendPageMarkdown(ctx context.Context, pageID, content string) (PageMarkdown, error) {
	body := map[string]any{
		"type": "insert_content",
		"insert_content": map[string]any{
			"content": content,
			// Sent explicitly: omitting it appends too, but that default is not
			// in the reference and is not worth depending on.
			"position": map[string]string{"type": "end"},
		},
	}
	var resp pageMarkdownResponse
	path := "/v1/pages/" + url.PathEscape(pageID) + "/markdown"
	if err := c.doRejectRetryable(ctx, http.MethodPatch, path, body, &resp); err != nil {
		return PageMarkdown{}, err
	}
	return resp.toPageMarkdown(), nil
}
```

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/notion/ -run TestAppendPageMarkdown -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Format, vet, full suite**

Run: `gofmt -w internal/notion/markdown.go internal/notion/markdown_test.go && go vet ./... && go test ./...`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/notion/markdown.go internal/notion/markdown_test.go
git commit -m "feat(notion): append to a page body without destroying it

insert_content adds to the end and leaves existing content alone -- the
non-destructive counterpart to the append-then-delete replace path.

The append is not idempotent, so it goes through doRejectRetryable: a
500 comes back as ErrAmbiguousWrite with no retry, because retrying
blindly would duplicate content on the user's page. A 429 is a refusal
and is retried."
```

---

### Task 3: `service.GetBody` (read path)

**Files:**
- Modify: `internal/service/service.go`
- Test: `internal/service/service_test.go`

**Interfaces:**
- Consumes: `Client.GetPageMarkdown` (Task 1), `Service.client`, `ErrEmptyPageID`. Deliberately **not** `notion.NormalizePageID` — see the note in Step 3.
- Produces: `func (s *Service) GetBody(ctx context.Context, pageID string) (notion.PageMarkdown, error)`

Design note (refines spec §2.2): `get`'s three addressing paths already return a `notion.Page` carrying the resolved id, so `GetBody` takes the id and does **not** re-resolve addressing. The CLI reuses the page it already fetched — one lookup, not two.

- [ ] **Step 1: Write the failing test**

Append to `internal/service/service_test.go`:

```go
func TestGetBodyReturnsPageMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/pages/abc/markdown" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"object":"page_markdown","id":"abc","markdown":"# Hi",
			"truncated":false,"unknown_block_ids":[]}`))
	}))
	defer srv.Close()

	svc := New(notion.New("tok", notion.WithBaseURL(srv.URL)), config.Profile{})
	got, err := svc.GetBody(context.Background(), "abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Markdown != "# Hi" {
		t.Errorf("markdown = %q", got.Markdown)
	}
}

func TestGetBodyRejectsEmptyPageID(t *testing.T) {
	svc := New(notion.New("tok"), config.Profile{})
	if _, err := svc.GetBody(context.Background(), ""); err == nil {
		t.Fatal("an empty page id must be an error")
	}
}
```

Check the existing imports of `service_test.go` and add only what is missing (`net/http`, `net/http/httptest`, `context`, and the `notion`/`config` packages).

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/service/ -run TestGetBody -v`
Expected: FAIL — `svc.GetBody` undefined.

- [ ] **Step 3: Write the implementation**

Add to `internal/service/service.go`, next to `GetByID`:

```go
// GetBody returns the page body as Markdown.
//
// It takes an already-resolved page id rather than re-running the addressing
// dance: get's --ticket/--id/--page-id paths each hand back a notion.Page that
// carries the id, so the caller reuses that instead of paying for a second
// lookup.
//
// It deliberately does NOT call NormalizePageID. That function parses what a
// *user* typed -- a URL, a bare 32-hex id, a dashed UUID -- and rejects
// anything else; the id here comes from a page Notion already returned, so it
// is canonical by construction. Running it through the user-input parser would
// reject perfectly good ids that simply are not 32 hex characters.
func (s *Service) GetBody(ctx context.Context, pageID string) (notion.PageMarkdown, error) {
	if pageID == "" {
		return notion.PageMarkdown{}, ErrEmptyPageID
	}
	return s.client.GetPageMarkdown(ctx, pageID)
}
```

> **Why this matters:** `NormalizePageID` (`internal/notion/pageid.go:30`) returns
> `ErrMalformedPageID` for anything that is not a URL, 32-hex or dashed UUID. The
> shared test fixture `cliRowJSON` (`internal/cli/get_test.go:92`) uses `"id":"page1"`,
> which it would reject — turning every `get --body` test in Task 4 into an exit-2
> failure. The append path never normalized, which is why only reading would have
> broken. Keep both paths consistent: no normalization on an id that came back from
> Notion.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/service/ -run TestGetBody -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Format, vet, full suite**

Run: `gofmt -w internal/service/service.go internal/service/service_test.go && go vet ./... && go test ./...`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "feat(service): expose the page body to callers

Takes an already-resolved page id: get's three addressing paths each
return a page that carries one, so reusing it costs one lookup instead
of two."
```

---

### Task 4: `get --body` / `--body-only`

**Files:**
- Modify: `internal/cli/get.go`
- Test: `internal/cli/get_test.go`

**Interfaces:**
- Consumes: `Service.GetBody` (Task 3), `notion.PageMarkdown` (Task 1), `printJSON` (`internal/cli/output.go`), `printWarnings` (`internal/cli/body.go`), `toPageJSON`, `withStubbedAPI` (test helper).
- Produces: the `body` JSON key — `{"markdown": string, "truncated": bool, "unknown_block_ids": []string}` — consumed by nothing else in this plan, but public API from here on.

- [ ] **Step 1: Write the failing tests**

These follow `get_test.go`'s existing style exactly: `executeArgs` returns an exit
code, `captureStdout`/`captureStderr` swap the real streams, and the stub must
answer the `/v1/data_sources/ds1` schema request as well as the query. Reuse the
`cliSchemaJSON` and `cliRowJSON` fixtures already in the file (`get_test.go:85`
and `:92`) — the row they describe is ticket `BDF-231`.

Append to `internal/cli/get_test.go`:

```go
// stubPageAndMarkdown answers the schema request, the row query and the body
// read, so a `get --body` run has everything it asks for.
func stubPageAndMarkdown(t *testing.T, markdown string, truncated bool, unknown []string) string {
	t.Helper()
	unknownJSON, err := json.Marshal(unknown)
	if err != nil {
		t.Fatalf("marshalling unknown ids: %v", err)
	}
	mdJSON, err := json.Marshal(markdown)
	if err != nil {
		t.Fatalf("marshalling markdown: %v", err)
	}
	return withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case strings.HasSuffix(r.URL.Path, "/markdown"):
			fmt.Fprintf(w, `{"object":"page_markdown","id":"page1","markdown":%s,
				"truncated":%t,"unknown_block_ids":%s}`, mdJSON, truncated, unknownJSON)
		default:
			w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
		}
	})
}

func TestGetBodyPrintsMarkdownAfterTheRow(t *testing.T) {
	cfg := stubPageAndMarkdown(t, "# Title\n\nSome body.", false, nil)
	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--body", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if !strings.Contains(out, "Hardening") {
		t.Errorf("the row line should still be printed, got:\n%s", out)
	}
	if !strings.Contains(out, "# Title") {
		t.Errorf("the body should be printed, got:\n%s", out)
	}
}

// --body-only exists so `> notes.md` yields a valid Markdown file: nothing but
// the body may reach stdout.
func TestGetBodyOnlyPrintsNothingButTheBody(t *testing.T) {
	cfg := stubPageAndMarkdown(t, "# Title\n\nSome body.", false, nil)
	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--body-only", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if out != "# Title\n\nSome body.\n" {
		t.Errorf("stdout must carry the body alone, got:\n%q", out)
	}
}

// A truncated page that looks complete is the failure this guards against --
// and the warning must not land on stdout, which may be redirected to a file.
func TestGetBodyWarnsAboutTruncationOnStderr(t *testing.T) {
	cfg := stubPageAndMarkdown(t, "partial", true, []string{"b1"})
	var out string
	errOut := captureStderr(t, func() {
		out = captureStdout(t, func() {
			if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--body-only", "--config", cfg}); code != ExitOK {
				t.Fatalf("exit code = %d", code)
			}
		})
	})
	if !strings.Contains(errOut, "truncated") {
		t.Errorf("truncation must be warned about on stderr, got:\n%s", errOut)
	}
	if strings.Contains(out, "truncated") {
		t.Errorf("the warning must not pollute stdout, got:\n%s", out)
	}
}

func TestGetBodyJSONAddsBodyKeyAndKeepsPageKeys(t *testing.T) {
	cfg := stubPageAndMarkdown(t, "# Title", false, nil)
	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--body", "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	var got struct {
		Page map[string]any `json:"page"`
		Body struct {
			Markdown        string   `json:"markdown"`
			Truncated       bool     `json:"truncated"`
			UnknownBlockIDs []string `json:"unknown_block_ids"`
		} `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not the expected JSON: %v\n%s", err, out)
	}
	if got.Body.Markdown != "# Title" {
		t.Errorf("body.markdown = %q", got.Body.Markdown)
	}
	// pageJSON is public API: its keys must survive under "page".
	for _, k := range []string{"id", "ticket", "title", "status", "page_id", "url"} {
		if _, ok := got.Page[k]; !ok {
			t.Errorf("page.%s went missing from --json output", k)
		}
	}
}

// Without --body the JSON shape must be exactly what it was before this
// feature: pageJSON at the top level, no "page"/"body" wrapper.
func TestGetWithoutBodyKeepsLegacyJSONShape(t *testing.T) {
	cfg := stubPageAndMarkdown(t, "unused", false, nil)
	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if _, wrapped := got["page"]; wrapped {
		t.Error(`without --body the JSON must stay flat: no "page" wrapper`)
	}
	if _, ok := got["ticket"]; !ok {
		t.Error("ticket must remain a top-level key")
	}
}

func TestGetBodyAndBodyOnlyAreMutuallyExclusive(t *testing.T) {
	cfg := stubPageAndMarkdown(t, "x", false, nil)
	if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--body", "--body-only", "--config", cfg}); code != ExitUsage {
		t.Fatalf("--body with --body-only must exit %d, got %d", ExitUsage, code)
	}
}

// --body-only means "the body and nothing else" in JSON too: no page wrapper,
// otherwise the flag silently degrades to --body exactly where a script relies
// on it.
func TestGetBodyOnlyJSONOmitsThePageWrapper(t *testing.T) {
	cfg := stubPageAndMarkdown(t, "# Title", false, nil)
	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--body-only", "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if _, wrapped := got["page"]; wrapped {
		t.Error("--body-only --json must not carry the page wrapper")
	}
	if got["markdown"] != "# Title" {
		t.Errorf("markdown = %v", got["markdown"])
	}
}

// A page with no content prints nothing at all -- not a stray blank line.
func TestGetBodyOnlyOnEmptyPagePrintsNothing(t *testing.T) {
	cfg := stubPageAndMarkdown(t, "", false, nil)
	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--body-only", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if out != "" {
		t.Errorf("an empty body must print nothing, got %q", out)
	}
}
```

Add `"fmt"` to the test file's imports if it is not already there.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/cli/ -run 'TestGetBody|TestGetWithoutBody' -v`
Expected: FAIL — unknown flags `--body` / `--body-only`.

- [ ] **Step 3: Write the implementation**

In `internal/cli/get.go`, add the two variables next to the existing ones in `newGetCmd`:

```go
	var withBody bool
	var bodyOnly bool
```

Register the flags next to the others, before `MarkFlagsMutuallyExclusive("ticket", …)`:

```go
	cmd.Flags().BoolVar(&withBody, "body", false,
		"also read the page body back, as Markdown")
	cmd.Flags().BoolVar(&bodyOnly, "body-only", false,
		"print only the page body, so redirecting to a file yields valid Markdown")
	cmd.MarkFlagsMutuallyExclusive("body", "body-only")
```

Inside `RunE`, after `profile := svc.Profile()`, insert the body read and the two output paths. `bodyOnly` implies reading the body, hence the `||`:

```go
	var body *notion.PageMarkdown
	if withBody || bodyOnly {
		md, err := svc.GetBody(cmd.Context(), page.ID)
		if err != nil {
			return err
		}
		body = &md
		// stderr, never stdout: --body-only is meant to be redirected into a
		// file, and a warning belongs in the terminal, not in that file.
		printWarnings(cmd.ErrOrStderr(), bodyWarnings(md))
	}

	if bodyOnly {
		// --body-only means "the body and nothing else" in both forms. With
		// --json that is the body object alone, unwrapped: degrading it to the
		// same output as --body would make the flag a no-op precisely where a
		// script relies on it.
		if asJSON {
			return printJSON(cmd.OutOrStdout(), toBodyJSON(*body))
		}
		cmd.Print(ensureTrailingNewline(body.Markdown))
		return nil
	}
	if asJSON && body != nil {
		// Only the --body form nests under "page": without it the flat
		// pageJSON shape every existing script parses must stay untouched.
		return printJSON(cmd.OutOrStdout(), map[string]any{
			"page": toPageJSON(page, profile.Properties),
			"body": toBodyJSON(*body),
		})
	}
```

Leave the existing `if asJSON { … }` block that follows exactly as it is: it now handles only the no-`--body` case.

Then, after the existing human-readable `cmd.Printf` calls, the body has to be printed too. Restructure the tail of `RunE` so both branches fall through to one body print instead of returning early:

```go
	if ticketIsTitle(profile.Properties) {
		cmd.Printf("%s%s  [%s]%s%s\n  %s\n",
			id, page.Properties[profile.Properties.Title].Text, status,
			priority, assignee, page.URL)
	} else {
		cmd.Printf("%s%s  %s  [%s]%s%s\n  %s\n",
			id,
			page.Properties[profile.Properties.Ticket].Text,
			page.Properties[profile.Properties.Title].Text,
			status, priority, assignee, page.URL)
	}
	// The emptiness check is on the outside: a page with no content should add
	// nothing at all, not a blank separator line followed by nothing.
	if body != nil && body.Markdown != "" {
		cmd.Print("\n" + ensureTrailingNewline(body.Markdown))
	}
	return nil
```

Add the three helpers at the end of `get.go`:

```go
// bodyJSON is the scripting shape of a page body. Like pageJSON, treat its
// keys as public API.
type bodyJSON struct {
	Markdown string `json:"markdown"`
	// Truncated reports that Notion cut the page off at its block ceiling:
	// the Markdown is real but incomplete.
	Truncated bool `json:"truncated"`
	// UnknownBlockIDs lists blocks Notion cannot render as Markdown. Always
	// present, empty rather than null, so a script never branches on absence.
	UnknownBlockIDs []string `json:"unknown_block_ids"`
}

func toBodyJSON(md notion.PageMarkdown) bodyJSON {
	ids := md.UnknownBlockIDs
	if ids == nil {
		ids = []string{}
	}
	return bodyJSON{Markdown: md.Markdown, Truncated: md.Truncated, UnknownBlockIDs: ids}
}

// bodyWarnings turns the two lossy signals into human-readable warnings. Both
// mean "what you are reading is not the whole page", which a reader who is not
// told would never suspect.
func bodyWarnings(md notion.PageMarkdown) []string {
	var out []string
	if md.Truncated {
		out = append(out, "the page is too large to render in full: this body is truncated")
	}
	if n := len(md.UnknownBlockIDs); n > 0 {
		out = append(out, fmt.Sprintf(
			"%d block(s) have no Markdown representation and appear as <unknown/>; "+
				"they are still on the page", n))
	}
	return out
}

// ensureTrailingNewline keeps piped output well-formed without adding a blank
// line to a body that already ends in one. An empty body stays empty: a page
// with no content prints nothing rather than a stray newline.
func ensureTrailingNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
```

Add `"fmt"` and `"strings"` to `get.go`'s imports (`notion` and `config` are already imported).

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/cli/ -run 'TestGetBody|TestGetWithoutBody' -v`
Expected: PASS (6 tests).

- [ ] **Step 5: Format, vet, full suite**

Run: `gofmt -w internal/cli/get.go internal/cli/get_test.go && go vet ./... && go test ./...`

Expected: everything passes **except** `TestEveryAgentFacingFlagIsDocumented`, which
now fails naming `--body` and `--body-only`. That is correct and expected: the test
derives the flag list from the binary and demands SKILL.md document each one. Task 10
closes it. Do not touch the skill or the test to silence it now.

`TestGetWithoutBodyKeepsLegacyJSONShape` is the regression guard on the flat JSON shape — if it fails, the wrapper leaked into the no-`--body` path.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/get.go internal/cli/get_test.go
git commit -m "feat(get): read a page body back with --body / --body-only

Until now the only way to see what --body-file was about to overwrite
was to open the page in a browser, which also left the agent skill's own
'read before you write' rule inapplicable to the body.

--body-only prints nothing but the Markdown, so redirecting it into a
file yields a valid document; truncation and unrenderable blocks are
warned about on stderr so they never contaminate that file. The flat
--json shape is unchanged without --body: the wrapper is opt-in."
```

---

### Task 5: append plumbing in `service`

**Files:**
- Modify: `internal/service/service.go`
- Test: `internal/service/service_test.go`

**Interfaces:**
- Consumes: `Client.AppendPageMarkdown` (Task 2), existing `BodyRequest`, `BodyResult`, `withBody`, `replaceBody`.
- Produces: `BodyRequest.AppendMarkdown string` — when non-empty, `withBody` appends instead of replacing. `BodyResult.WasAppend bool` records which mode ran; `BodyResult.Appended bool` records whether it landed.

**Refines spec §2.2**, which sketched a bare `Append: true` flag. Carrying the Markdown
in the field itself keeps "what to append" and "append rather than replace" from being
two pieces of state that can disagree. The two result booleans are likewise a refinement:
one flag cannot answer both "which operation was this?" and "did it succeed?", and the
error path needs the first to report the right counters.

- [ ] **Step 1: Write the failing tests**

Append to `internal/service/service_test.go`:

```go
// An append must never delete: the whole point of the mode.
func TestWithBodyAppendsWithoutDeletingAnything(t *testing.T) {
	var deletes, patches int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			atomic.AddInt32(&deletes, 1)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/markdown"):
			atomic.AddInt32(&patches, 1)
			_, _ = w.Write([]byte(`{"object":"page_markdown","id":"p1","markdown":"old\nnew",
				"truncated":false,"unknown_block_ids":[]}`))
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	svc := New(notion.New("tok", notion.WithBaseURL(srv.URL)), config.Profile{})
	res, err := svc.withBody(context.Background(), notion.Page{ID: "p1"}, "updated",
		&BodyRequest{AppendMarkdown: "new"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if atomic.LoadInt32(&patches) != 1 {
		t.Errorf("want exactly 1 append call, got %d", patches)
	}
	if n := atomic.LoadInt32(&deletes); n != 0 {
		t.Fatalf("an append must delete nothing, got %d DELETEs", n)
	}
	if res.Body == nil || !res.Body.Appended {
		t.Error("the result must report that an append happened")
	}
}

// A failed append still went out after the properties were written, so it must
// surface as a BodyWriteError like the replace path does.
func TestWithBodyAppendFailureIsABodyWriteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"object":"error","status":500,"code":"internal_server_error","message":"boom"}`))
	}))
	defer srv.Close()

	svc := New(notion.New("tok", notion.WithBaseURL(srv.URL)), config.Profile{})
	_, err := svc.withBody(context.Background(), notion.Page{ID: "p1"}, "updated",
		&BodyRequest{AppendMarkdown: "new"})
	var bwe *BodyWriteError
	if !errors.As(err, &bwe) {
		t.Fatalf("want *BodyWriteError, got %v", err)
	}
	if !errors.Is(err, notion.ErrAmbiguousWrite) {
		t.Errorf("the ambiguity must stay reachable through the wrapper: %v", err)
	}
	// "re-run to converge" is right for a replace and dangerous for an append:
	// re-running one that did land duplicates the note.
	if !strings.Contains(err.Error(), "check the page before re-running") {
		t.Errorf("an ambiguous append must warn against blind re-running, got: %v", err)
	}
}
```

Add `"sync/atomic"`, `"strings"` and `"errors"` to the test imports if missing.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/service/ -run TestWithBody -v`
Expected: FAIL — `BodyRequest` has no field `AppendMarkdown`.

- [ ] **Step 3: Write the implementation**

In `internal/service/service.go`, extend `BodyRequest` (keep the existing comment above it):

```go
type BodyRequest struct {
	Blocks   []notion.Block
	Progress io.Writer // optional; ephemeral progress lines go here (stderr)
	// AppendMarkdown, when non-empty, appends this Markdown to the end of the
	// page instead of replacing the body: Blocks is ignored and nothing is
	// deleted. The two modes are mutually exclusive and the CLI enforces that,
	// so exactly one of Blocks / AppendMarkdown is ever set.
	AppendMarkdown string
}
```

Extend `BodyResult`:

```go
type BodyResult struct {
	BlocksWritten int
	BlocksDeleted int
	Warnings      []string // e.g. skipped child_page/child_database
	// WasAppend records which mode ran, set before the call so it survives a
	// failure. Appended records whether that append actually landed. Two flags
	// rather than one because the error path has to answer both questions: a
	// single false would not distinguish "the append failed" from "this was a
	// replace", and the two want different counters reported.
	WasAppend bool
	Appended  bool
}
```

Add `appendBody` next to `replaceBody`:

```go
// appendBody adds req.AppendMarkdown to the end of the page, deleting nothing.
// Unlike replaceBody it makes exactly one call, so there is no partially
// applied state to converge: it either appended or it did not.
func (s *Service) appendBody(ctx context.Context, pageID string, req *BodyRequest) (BodyResult, error) {
	// WasAppend is set here, before the call, so it survives a failure: the
	// error path has to know which mode ran in order to report the right
	// counters.
	res := BodyResult{WasAppend: true}
	progress(req.Progress, "appending to the page body…")
	if _, err := s.client.AppendPageMarkdown(ctx, pageID, req.AppendMarkdown); err != nil {
		// ErrAmbiguousWrite reads "re-run to converge", which is sound advice
		// for the replace path -- re-running it makes the body equal the file
		// again -- and the wrong advice here: re-running an append that did
		// land appends the note twice. Say so, rather than let the generic
		// wording send someone to duplicate their own content.
		if errors.Is(err, notion.ErrAmbiguousWrite) {
			return res, fmt.Errorf(
				"the append may or may not have been applied; check the page before re-running, "+
					"because re-running an append that did land adds the content twice: %w", err)
		}
		return res, err
	}
	res.Appended = true
	return res, nil
}
```

`errors` and `fmt` are already imported by `service.go`.

In `withBody`, dispatch on the mode — replace the single `replaceBody` call with:

```go
	var br BodyResult
	var err error
	if body.AppendMarkdown != "" {
		br, err = s.appendBody(ctx, page.ID, body)
	} else {
		br, err = s.replaceBody(ctx, page.ID, body)
	}
	res.Body = &br
	if err != nil {
		return res, &BodyWriteError{err: err}
	}
	return res, nil
```

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/service/ -run TestWithBody -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Format, vet, full suite**

Run: `gofmt -w internal/service/service.go internal/service/service_test.go && go vet ./... && go test ./...`
Expected: all PASS — the existing `--body-file` tests must be untouched and green.

- [ ] **Step 6: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "feat(service): append a body instead of replacing it

One call, nothing deleted, so unlike the replace path there is no
half-applied state to converge. A failure still wraps in BodyWriteError:
it happened after the properties were written, which is exactly what
that type is for."
```

---

### Task 6: `--append-file` on `upsert` and `set`

**Files:**
- Modify: `internal/cli/upsert.go` (flag registration in `bindShared`), `internal/cli/body.go` (loader), `internal/cli/set.go`, `internal/cli/upsert.go` (call sites)
- Test: `internal/cli/body_test.go`

**Interfaces:**
- Consumes: `BodyRequest.AppendMarkdown` (Task 5), `readBodySource`, `template.Expand`, `maxBodyFileBytes`, `Errorf`/`ExitUsage`, `emitWrite`.
- Produces: `func loadAppendBody(path string, stdin io.Reader, progress io.Writer, vars map[string]string) (*service.BodyRequest, error)`; `writeFlags.appendFile string`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/body_test.go`:

```go
func TestLoadAppendBodyKeepsRawMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte("## Update\nDone."), 0o600); err != nil {
		t.Fatal(err)
	}
	req, err := loadAppendBody(path, nil, io.Discard, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Raw Markdown, not blocks: the append endpoint parses it server-side.
	if req.AppendMarkdown != "## Update\nDone." {
		t.Errorf("AppendMarkdown = %q", req.AppendMarkdown)
	}
	if len(req.Blocks) != 0 {
		t.Errorf("the append path must not build blocks, got %d", len(req.Blocks))
	}
}

// Notion answers 200 and does nothing for empty content (spec §10.e), so this
// check is the only thing between a user and a successful-looking no-op.
func TestLoadAppendBodyRejectsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(path, []byte("   \n\t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAppendBody(path, nil, io.Discard, nil); err == nil {
		t.Fatal("an empty append file must be a usage error, not a silent no-op")
	}
}

func TestLoadAppendBodyExpandsPlaceholders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte("Ticket {{ticket}} done."), 0o600); err != nil {
		t.Fatal(err)
	}
	req, err := loadAppendBody(path, nil, io.Discard, map[string]string{"ticket": "T-9", "date": "2026-08-04"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.AppendMarkdown != "Ticket T-9 done." {
		t.Errorf("AppendMarkdown = %q", req.AppendMarkdown)
	}
}

func TestLoadAppendBodyReadsStdin(t *testing.T) {
	req, err := loadAppendBody("-", strings.NewReader("from stdin"), io.Discard, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.AppendMarkdown != "from stdin" {
		t.Errorf("AppendMarkdown = %q", req.AppendMarkdown)
	}
}

// A file that is non-empty on disk but expands to nothing would slip past the
// pre-expansion check and reach Notion as a silent no-op.
func TestLoadAppendBodyRejectsFileThatExpandsToNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte("{{ticket}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Addressing by --page-id leaves ticket empty, which is what makes this
	// reachable in practice.
	_, err := loadAppendBody(path, nil, io.Discard, map[string]string{"ticket": "", "date": "2026-08-04"})
	if err == nil {
		t.Fatal("a file that expands to nothing must be rejected, not sent as an empty append")
	}
}

// The empty-file check must stop the run BEFORE any network call: Notion
// answers 200 for an empty append, so reaching it at all defeats the guard.
func TestSetAppendEmptyFileMakesNoHTTPCall(t *testing.T) {
	var calls int32
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := executeArgs([]string{"set", "--ticket", "BDF-231", "--append-file", empty, "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("an empty append must be rejected before any request, got %d calls", n)
	}
}

func TestSetRejectsBodyFileWithAppendFile(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code := executeArgs([]string{"set", "--ticket", "BDF-231",
		"--body-file", a, "--append-file", b, "--config", cfg})
	if code != ExitUsage {
		t.Fatalf("--body-file with --append-file must exit %d (replace and append are different intents), got %d",
			ExitUsage, code)
	}
}
```

Add any missing imports to the test file — `body_test.go`'s current import block does **not** include `sync/atomic` or `net/http`, both needed here, alongside `os`, `io`, `strings` and `path/filepath`.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/cli/ -run 'TestLoadAppendBody|TestSetRejectsBodyFile' -v`
Expected: FAIL — `loadAppendBody` undefined.

- [ ] **Step 3: Write the implementation**

First, make `--dry-run` tell the truth about an append. Today `blockCount`
(`internal/service/service.go:536`) counts only `len(body.Blocks)`, and `emitPlan`
(`internal/cli/body.go:106`) prints the body line only when `BodyBlocks > 0` — so
`set --append-file note.md --dry-run` would report the properties and stay **completely
silent about the append**, in the one command whose entire job is saying what it would do.

Add a field to `Plan` in `internal/service/plan.go`, next to `BodyBlocks` (`plan.go:25`):

```go
	// AppendBytes is how many bytes of Markdown --append-file would add. Bytes
	// rather than blocks: the append path never parses into blocks, so there is
	// no block count to report.
	AppendBytes int `json:"append_bytes,omitempty"`
```

`planFor` (`plan.go:47`) takes `bodyBlocks int` and has four call sites
(`service.go:466`, `:523`, `:651`, plus its definition). Rather than widen that
signature in every one, set the field on the returned plan inside `planFor` — it
already receives everything else it needs — by giving it the append length through the
same argument path. The smallest change that stays honest: add one parameter
`appendBytes int` to `planFor`, pass `appendCount(body)` at each of the three call
sites, and add the counterpart to `blockCount` in `service.go`:

```go
// appendCount is blockCount for the append path: bytes of Markdown, since
// nothing is parsed into blocks there.
func appendCount(body *BodyRequest) int {
	if body == nil {
		return 0
	}
	return len(body.AppendMarkdown)
}
```

Then in `emitPlan` (`internal/cli/body.go:106`), after the existing `BodyBlocks` line:

```go
	if plan.AppendBytes > 0 {
		cmd.Printf("  %-20s %d bytes (appending to the current body)\n", "page body", plan.AppendBytes)
	}
```

Add a test asserting `set --ticket BDF-231 --append-file note.md --dry-run` mentions the
append in its output and makes **no** PATCH to `/markdown` (count requests in the stub,
like `TestSetAppendEmptyFileMakesNoHTTPCall` above).

Next, in `internal/cli/body.go`, add after `loadBody`:

```go
// loadAppendBody reads a --append-file into a BodyRequest that appends rather
// than replaces. Unlike loadBody it does NOT parse Markdown into blocks: the
// append endpoint takes Markdown directly and parses it server-side, so
// goldmark and ValidateAppendable are not in this path at all.
//
// The empty check is load-bearing rather than defensive: Notion answers 200 and
// does nothing for empty content, so without it an empty file would report
// success while changing nothing.
func loadAppendBody(path string, stdin io.Reader, progress io.Writer, vars map[string]string) (*service.BodyRequest, error) {
	raw, err := readBodySource(path, stdin)
	if err != nil {
		return nil, Errorf(ExitUsage, "reading append file %s: %v", path, err)
	}
	if len(raw) > maxBodyFileBytes {
		return nil, Errorf(ExitUsage, "append file %s is over the %d-byte limit", path, maxBodyFileBytes)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil, Errorf(ExitUsage, "append file %s is empty", path)
	}
	if vars != nil {
		expanded, err := template.Expand(string(raw), vars)
		if err != nil {
			return nil, Errorf(ExitUsage, "%s: %v", path, err)
		}
		raw = []byte(expanded)
		// Checked again AFTER expanding, not only before: a file holding just
		// "{{ticket}}" is non-empty on disk but expands to nothing when the row
		// was addressed by --page-id or --id, and would reach Notion as the
		// silent no-op the first check exists to prevent.
		if strings.TrimSpace(string(raw)) == "" {
			return nil, Errorf(ExitUsage,
				"append file %s expands to nothing: every placeholder in it resolved to an empty value", path)
		}
	}
	return &service.BodyRequest{AppendMarkdown: string(raw), Progress: progress}, nil
}
```

In `internal/cli/upsert.go`, add the field to `writeFlags`:

```go
	appendFile string
```

and register it in `bindShared`, right after the `--body-file` registration:

```go
	cmd.Flags().StringVar(&wf.appendFile, "append-file", "",
		"Markdown file added to the END of the page body ('-' for stdin); "+
			"appends, deletes nothing")
	// Replace and append are different intents, and silently letting one win
	// is how a user loses a page body they meant to keep.
	cmd.MarkFlagsMutuallyExclusive("body-file", "append-file")
```

In **both** `internal/cli/set.go` and `internal/cli/upsert.go`, extend the block that builds `body`. In `set.go` it currently reads `if wf.bodyFile != "" { … }`; make it:

```go
			switch {
			case wf.bodyFile != "":
				body, warnings, err = loadBody(wf.bodyFile, cmd.InOrStdin(), cmd.ErrOrStderr(), wf.bodyVars())
				if err != nil {
					return err
				}
			case wf.appendFile != "":
				body, err = loadAppendBody(wf.appendFile, cmd.InOrStdin(), cmd.ErrOrStderr(), wf.bodyVars())
				if err != nil {
					return err
				}
			}
```

Apply the same change at `upsert.go`'s equivalent call site (around `upsert.go:126`), matching whatever variable names are already in scope there.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/cli/ -run 'TestLoadAppendBody|TestSetRejectsBodyFile' -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Format, vet, full suite**

Run: `gofmt -w internal/cli/body.go internal/cli/upsert.go internal/cli/set.go internal/cli/body_test.go && go vet ./... && go test ./...`

Expected: everything passes **except** `TestEveryAgentFacingFlagIsDocumented`, which
now also names `--append-file` alongside the two flags from Task 4. Still expected;
Task 10 closes it.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/body.go internal/cli/upsert.go internal/cli/set.go internal/cli/body_test.go
git commit -m "feat(write): add --append-file to upsert and set

Adds to the end of a page body and deletes nothing -- what you want when
the intent is 'record this', not 'this file is now the page'. It carries
--expand and the 1 MiB cap over from --body-file, but skips the Markdown
parser entirely: the endpoint takes Markdown directly.

Mutually exclusive with --body-file. Silently letting one win is how
somebody loses a body they meant to keep.

An empty file is rejected before the call, because Notion answers 200
and does nothing for empty content."
```

---

### Task 7: report the append in `--json`

**Files:**
- Modify: `internal/cli/body.go` (`emitWrite`)
- Test: `internal/cli/body_test.go`

**Interfaces:**
- Consumes: `BodyResult.Appended` (Task 5), `emitWrite`.
- Produces: `body: {"appended": true}` on a successful append; the replace path keeps `blocks_written` / `blocks_deleted`.

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/body_test.go`:

```go
// appended and blocks_written are different facts about different operations:
// a script must not have to infer which one ran.
func TestEmitWriteJSONReportsAppendDistinctly(t *testing.T) {
	cmd := &cobra.Command{}
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)

	res := service.Result{
		Action: "updated",
		Page:   notion.Page{ID: "p1", URL: "https://notion.so/p1"},
		Body:   &service.BodyResult{WasAppend: true, Appended: true},
	}
	if err := emitWrite(cmd, cliProps(), res, nil, true, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		Body map[string]any `json:"body"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if got.Body["appended"] != true {
		t.Errorf(`body.appended = %v, want true`, got.Body["appended"])
	}
	if _, present := got.Body["blocks_written"]; present {
		t.Error("an append must not report blocks_written: that is the replace path's counter")
	}
}

// The replace path must keep reporting exactly what it always did.
func TestEmitWriteJSONKeepsReplaceCounters(t *testing.T) {
	cmd := &cobra.Command{}
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)

	res := service.Result{
		Action: "updated",
		Page:   notion.Page{ID: "p1"},
		Body:   &service.BodyResult{BlocksWritten: 3, BlocksDeleted: 2},
	}
	if err := emitWrite(cmd, cliProps(), res, nil, true, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		Body map[string]any `json:"body"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if got.Body["blocks_written"] != float64(3) || got.Body["blocks_deleted"] != float64(2) {
		t.Errorf("replace counters changed: %v", got.Body)
	}
	if _, present := got.Body["appended"]; present {
		t.Error("a replace must not report appended")
	}
}

// A FAILED append must say appended:false -- not borrow the replace path's
// counters and claim "0 blocks written" about an operation that never counted
// blocks.
//
// Driven end to end rather than by handing emitWrite a *BodyWriteError:
// its field is unexported (service.go:181), so package cli cannot build one.
// This mirrors the existing partial-failure test in this file, which makes the
// properties write succeed and the body write fail.
func TestSetAppendPartialFailureReportsAppendedFalse(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/markdown"):
			// The append fails AFTER the properties were written.
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"object":"error","status":500,"code":"internal_server_error","message":"boom"}`))
		case r.Method == http.MethodPatch:
			w.Write([]byte(cliRowJSON)) // the properties write succeeds
		default:
			w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
		}
	})
	note := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(note, []byte("## Progress\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureStdout(t, func() {
		code = executeArgs([]string{"set", "--ticket", "BDF-231",
			"--append-file", note, "--json", "--config", cfg})
	})
	if code != ExitError {
		t.Fatalf("a partial failure must exit %d, got %d", ExitError, code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout must stay parsable JSON on partial failure: %v\n%s", err, out)
	}
	body, ok := got["body"].(map[string]any)
	if !ok {
		t.Fatalf("body missing: %v", got)
	}
	if body["appended"] != false {
		t.Errorf(`body.appended = %v, want false`, body["appended"])
	}
	if _, present := body["blocks_written"]; present {
		t.Error("a failed append must not report blocks_written")
	}
	if got["page"] == nil {
		t.Error("page (applied properties) must still be present")
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/cli/ -run TestEmitWriteJSON -v`
Expected: FAIL — the append case still emits `blocks_written: 0`.

- [ ] **Step 3: Write the implementation**

Two branches of `emitWrite` need it — the failure branch reports counters too — and
telling them apart needs one extra bit of state.

`Appended` alone cannot do it: it is false both for a *failed append* and for a
*replace*, so the failure branch could not tell which operation it is reporting on.
Add a second field to `BodyResult` in `internal/service/service.go` (alongside the
`Appended` field from Task 5):

```go
	// WasAppend records which operation ran, regardless of whether it
	// succeeded. Appended answers "did it land?"; this answers "which mode was
	// this?", which is what an error path needs in order to report the right
	// counters.
	WasAppend bool
```

Task 5 already adds this field and already sets it in `appendBody` before the call, so
it survives a failure — nothing to change there; this task only consumes it.

Now, in the **failure** branch (`internal/cli/body.go:137-153`), replace the inner
`if res.Body != nil` block with:

```go
			if res.Body != nil {
				if res.Body.WasAppend {
					// An append either landed or it did not; there are no
					// partial counts, and borrowing the replace path's would
					// claim "0 blocks written" about an operation that never
					// counted blocks.
					body["appended"] = res.Body.Appended
				} else {
					// Real counts of what happened before the failure: crucial in the
					// dual case (append ok, a DELETE failed) where the body WAS written
					// (spec §8).
					body["blocks_written"] = res.Body.BlocksWritten
					body["blocks_deleted"] = res.Body.BlocksDeleted
				}
			}
```

Then, in the **success** branch, replace the block that builds `out["body"]` with:

```go
	if asJSON {
		out := map[string]any{"action": res.Action, "page": toPageJSON(res.Page, props)}
		if res.Body != nil {
			// An append and a replace are different operations with different
			// consequences: report the counters that actually apply, so a
			// script never has to infer which one ran. Branch on WasAppend
			// (which mode ran), not on Appended (whether it landed).
			if res.Body.WasAppend {
				out["body"] = map[string]any{"appended": res.Body.Appended}
			} else {
				out["body"] = map[string]any{
					"blocks_written": res.Body.BlocksWritten,
					"blocks_deleted": res.Body.BlocksDeleted,
				}
			}
		}
		return printJSON(cmd.OutOrStdout(), out)
	}
```

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/cli/ -run TestEmitWriteJSON -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Format, vet, full suite**

Run: `gofmt -w internal/cli/body.go internal/cli/body_test.go && go vet ./... && go test ./...`
Expected: all PASS except the known `TestEveryAgentFacingFlagIsDocumented` failure carried from Task 4 (closed in Task 10).

- [ ] **Step 6: Commit**

```bash
git add internal/cli/body.go internal/cli/body_test.go
git commit -m "feat(json): report an append as appended, not as zero blocks

Reusing blocks_written for an append would report 0 blocks on a
successful write. They are different operations; the output says which
one ran."
```

---

### Task 8: end-to-end append test

**Files:**
- Test: `internal/cli/body_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–7.
- Produces: nothing — this task adds coverage only.

- [ ] **Step 1: Write the test**

Append to `internal/cli/body_test.go`:

```go
// The whole path, flag to HTTP: --append-file must reach Notion as one
// insert_content PATCH and delete nothing.
func TestSetAppendFileEndToEnd(t *testing.T) {
	var patched map[string]any
	var deletes int32
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case r.Method == http.MethodDelete:
			atomic.AddInt32(&deletes, 1)
			fmt.Fprint(w, `{}`)
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/markdown"):
			_ = json.NewDecoder(r.Body).Decode(&patched)
			fmt.Fprint(w, `{"object":"page_markdown","id":"page1","markdown":"old\nnew",
				"truncated":false,"unknown_block_ids":[]}`)
		case r.Method == http.MethodPatch:
			// the properties write
			fmt.Fprint(w, cliRowJSON)
		default:
			fmt.Fprint(w, `{"results":[`+cliRowJSON+`],"has_more":false}`)
		}
	})

	dir := t.TempDir()
	note := filepath.Join(dir, "note.md")
	if err := os.WriteFile(note, []byte("## Progress\nShipped."), 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"set", "--ticket", "BDF-231",
			"--append-file", note, "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	if patched == nil {
		t.Fatal("no append reached Notion")
	}
	if patched["type"] != "insert_content" {
		t.Errorf(`type = %v, want "insert_content"`, patched["type"])
	}
	ic, ok := patched["insert_content"].(map[string]any)
	if !ok {
		t.Fatalf("insert_content missing: %v", patched)
	}
	if ic["content"] != "## Progress\nShipped." {
		t.Errorf("content = %v", ic["content"])
	}
	if n := atomic.LoadInt32(&deletes); n != 0 {
		t.Fatalf("append must delete nothing, got %d DELETEs", n)
	}
	var got struct {
		Body map[string]any `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if got.Body["appended"] != true {
		t.Errorf("--json should report the append, got:\n%s", out)
	}
}
```

If the stub's routing does not match how `withStubbedAPI` is used elsewhere in this file, follow the existing convention rather than this sketch — the assertions are what matter.

- [ ] **Step 2: Run the test**

Run: `go test ./internal/cli/ -run TestSetAppendFileEndToEnd -v`
Expected: PASS. If it fails on stub routing, fix the stub, not the assertions.

- [ ] **Step 3: Format, vet, full suite**

Run: `gofmt -w internal/cli/body_test.go && go vet ./... && go test ./...`
Expected: all PASS except the known `TestEveryAgentFacingFlagIsDocumented` failure carried from Task 4 (closed in Task 10).

- [ ] **Step 4: Commit**

```bash
git add internal/cli/body_test.go
git commit -m "test(cli): pin the append path end to end

Flag to HTTP: one insert_content PATCH, zero DELETEs."
```

---

### Task 9: README (English and Italian)

**Files:**
- Modify: `README.md`, `README.it.md`

**Interfaces:**
- Consumes: the final flag surface from Tasks 4, 6, 7.
- Produces: nothing consumed by code.

- [ ] **Step 1: Correct the claim this feature falsifies**

`README.md:272` currently says the tool never reads a page body back:

> «There is no append mode and no undo, so treat the file as the single source of truth for that page — and note that **no command in this tool reads a page body back, `get` included**, so opening the page in Notion is the only way to see what a run would replace.»

Both halves are now false: there **is** an append mode, and `get --body` reads the body. Rewrite that passage to say instead that `--body-file` still owns the body and has no undo, that `get --body` shows what a run would replace, and that `--append-file` is the non-destructive alternative. Make the equivalent edit in `README.it.md`.

Find the matching Italian passage with:

```bash
grep -n "body" README.it.md | head -40
```

- [ ] **Step 2: Document reading the body**

Add a section next to `--body-file` in both files covering:
- `get --ticket <key> --body` and `--body-only`, and that both work with `--page-id` and `--id`.
- `--body-only` prints only Markdown so `> notes.md` yields a valid file.
- `--json` nests `page` and `body`; **without** `--body` the JSON shape is unchanged.
- `truncated` and `unknown_block_ids`, warned about on stderr.
- Unsupported blocks (bookmark, embed, link preview, breadcrumb, template button) read back as `<unknown/>`; the content is still on the page.
- Round-tripping is **not** lossless — file URLs in the returned Markdown are pre-signed and expire. Reading is for inspection, not for a download-edit-reupload cycle.

- [ ] **Step 3: Document appending**

Add a section covering:
- `--append-file` on `upsert` and `set`, `-` for stdin, `--expand` supported, 1 MiB cap.
- **Append semantics**: adds to the end, deletes nothing — as opposed to `--body-file`'s replace.
- Mutually exclusive with `--body-file`.
- An empty file is a usage error (exit 2), not a silent no-op.
- `--json` reports `body: {"appended": true}`.
- Not idempotent: running it twice appends twice. On an ambiguous failure the tool says the outcome is unknown and does **not** retry — check the page before re-running.

- [ ] **Step 4: Update the feature list and the "Implemented today" line**

Add reading and appending to the bullet list near the top of both READMEs, and to the `Implemented today:` paragraph (`README.md:581` and its Italian counterpart).

- [ ] **Step 5: Verify the claims you just wrote**

Run: `go run ./cmd/notion-track get --help` and `go run ./cmd/notion-track set --help`
Confirm every flag you documented exists with the wording you described. Fix the README, not the flags.

- [ ] **Step 6: Commit**

```bash
git add README.md README.it.md
git commit -m "docs: the tool can read a page body back now

The replace-semantics paragraph told readers that no command reads a
body back and that there is no append mode. Both are now false, so it
said the opposite of the truth in the one place someone checks before
overwriting a page.

Documents --body/--body-only and --append-file, including what the
Markdown API does not round-trip: expiring file URLs and blocks that
come back as <unknown/>."
```

---

### Task 10: agent skill

**Files:**
- Modify: `skills/notion-track/SKILL.md`

**Interfaces:**
- Consumes: the final flag surface.
- Produces: nothing consumed by code.

**This task is already failing before you start.** `TestEveryAgentFacingFlagIsDocumented`
(`internal/cli/skilldoc_test.go:69`) derives the flag list from the built binary and
asserts SKILL.md documents every one of them, so `--body`, `--body-only` and
`--append-file` have been breaking it since Tasks 4 and 6. The test needs no
extending — documenting the flags is what turns it green.

- [ ] **Step 1: Confirm the failure and read the harness**

Run: `go test ./internal/cli/ -run Skill -v`
Expected: FAIL — `TestEveryAgentFacingFlagIsDocumented` naming the three new flags.

Read `internal/cli/skilldoc_test.go` before editing: `TestGetJSONFieldsAreDocumented`
(`:127`) may also require the new `body` JSON key to be documented, and
`flagsMentionedIn` (`:164`) defines how a flag counts as "mentioned" — write the
skill so it matches that parser.

- [ ] **Step 2: Correct the claim this feature falsifies, and close the gap**

`skills/notion-track/SKILL.md:206-211` currently tells the agent, in bold:

> «**You cannot see what you are about to delete** — `get` returns properties only, and
> no command in this tool reads a page body. So: use `--body-file` freely on pages this
> workflow created, and on any pre-existing row **ask the user before overwriting**. "I
> ran `get` first" is not a check on the body.»

Every sentence of that is now wrong, and no test catches it (`skilldoc_test.go` checks
flags, subcommands, exit codes and JSON fields — not prose). Rewrite the passage: `get
--body` **is** the check on the body, and "I ran `get --body` first" is now exactly the
right thing to have done. Keep the caution about `--body-file` owning the body and having
no undo — that part is still true.

`SKILL.md:50-51` states the "Read before you write" rule that this makes applicable to
the body for the first time. In the `--body-file` section, add an explicit instruction:
before replacing a body that may hold content, inspect it with `get --ticket <key>
--body`. Phrase it as an instruction, not a footnote.

- [ ] **Step 3: Document reading, and prefer appending**

Add a section for `get --body` / `--body-only`, and one for `--append-file` stating that **when the intent is to add** (a progress note, a CI result) `--append-file` is the right tool, and `--body-file` is only for "this file is now the whole page". Note that:
- an append is not idempotent — running it twice appends twice;
- `<unknown/>` in a body read means "block with no Markdown form", **not** lost content. An agent must not try to "repair" the page over it, which would destroy real content.

- [ ] **Step 4: Update the frontmatter description**

Add content triggers in both languages, matching the existing style: `"leggi la pagina"`, `"cosa c'è scritto nel ticket"`, `"aggiungi una nota"`, `"append a note"`, `"read the page"`.

- [ ] **Step 5: Run the skill doc tests**

Run: `go test ./... -run Skill -v`
Expected: PASS — the failure from Step 1 is now closed. If a test still fails because SKILL.md and the binary disagree, fix whichever is actually wrong — do not weaken the assertion.

- [ ] **Step 6: Full suite**

Run: `go vet ./... && go test ./...`
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add skills/notion-track/SKILL.md
git commit -m "docs(skill): teach the agent to read a body before replacing it

The skill has always told the agent to read before writing, but for the
page body there was nothing to read with -- the rule was stated and
unusable. get --body closes that, and --append-file gives it a way to
add a note without owning the whole page.

Also warns that <unknown/> means 'block with no Markdown form', not
lost content: an agent that tries to repair the page over it would
destroy the real thing."
```

---

## Verification

After Task 10, the whole feature should hold together:

```bash
go vet ./... && go test ./...
go build -o /tmp/notion-track ./cmd/notion-track
/tmp/notion-track get --help | grep -E 'body|body-only'
/tmp/notion-track set --help | grep 'append-file'
```

Against a real board (the probe page from spec §10 has been archived; create a throwaway row rather than touching a real ticket):

```bash
notion-track get --ticket <throwaway> --body-only
notion-track set --ticket <throwaway> --append-file note.md --json
notion-track get --ticket <throwaway> --body-only    # the note is there, prior content intact
```
