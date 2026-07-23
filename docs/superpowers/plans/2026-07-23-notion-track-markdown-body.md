# Markdown page body (`--body-file`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `--body-file <path>` to `upsert` and `set` so a Markdown file becomes the Notion page body, with replace semantics (run twice → identical body).

**Architecture:** goldmark (+GFM) parses Markdown; a new pure `internal/markdown` package maps its AST to `notion.Block` values (owning content chunking: 2000-char rich-text split, >100-span block split). `internal/notion` gains block types with `MarshalJSON`, request-grouping + pre-flight validation, three block endpoints (list/append/delete), and a third retry mode that retries only on certain-rejection statuses. `internal/service.replaceBody` orchestrates snapshot→append→delete (skipping sub-pages). The CLI reads/validates the file pre-flight and shapes `--json`.

**Tech Stack:** Go 1.26, cobra, `net/http` stdlib client, `github.com/yuin/goldmark` (new prod dependency), stdlib `testing` + `net/http/httptest`.

## Global Constraints

Copied verbatim from the spec (`docs/superpowers/specs/2026-07-23-notion-track-markdown-body-design.md`) and the API note (`docs/superpowers/notes/2026-07-23-notion-api-version.md`). Every task inherits these:

- **API version** `2026-03-11`; body endpoint `PATCH /v1/blocks/{block_id}/children`, list `GET /v1/blocks/{block_id}/children` (paginated), delete `DELETE /v1/blocks/{block_id}` (archives).
- **Per-request limits:** ≤100 children per array, ≤2 nesting levels per request, ≤1000 blocks and ≤500KB per payload. `rich_text.text.content` ≤2000 chars; rich_text array ≤100 elements. Byte budget uses **450 KiB** margin.
- **`position: {"type":"end"}`** on append; chunk appends are **strictly sequential** (order depends on it).
- **Retry:** retry ONLY `429`/`503`/`529` (certain rejection), honoring `Retry-After`; `500`/`502`/`504`/transport errors are ambiguous → `ErrAmbiguousWrite`, no retry. `AppendBlockChildren` is non-idempotent.
- **Replace order:** snapshot children → append new body → delete old children, **skipping `child_page`/`child_database`**; a `404` on delete is success.
- **Pre-flight before any network:** read file (`-` = stdin), reject missing/unreadable/empty (exit 2), reject file >1 MiB (exit 2), `ToBlocks`, `ValidateAppendable` (exit 2). Warnings → **stderr**; `--json` → stdout.
- **`--json` additive:** `body` object only when `--body-file` used; partial failure (properties written, body failed) → exit **1** with `body.error`.
- **No third-party test frameworks.** TDD (RED→GREEN), `go test ./... -race`, a `*_test.go` beside each production file. Docs/comments in English; this plan and the spec are Italian.
- **Never add `Co-Authored-By` to commits.**

Branch: `v0.3-markdown-body` (already created; spec already committed).

---

## File Structure

**Create:**
- `internal/notion/block.go` / `block_test.go` — `Block`, `Span`, `MarshalJSON`.
- `internal/notion/blocks.go` / `blocks_test.go` — `ChildBlock`, `ListBlockChildren`, `AppendBlockChildren`, `DeleteBlock`, `ValidateAppendable`, `splitIntoRequests`, counting helpers, `doRejectRetryable`, `rejectedByServer`.
- `internal/markdown/language.go`, `internal/markdown/split.go` (+ tests) — pure helpers.
- `internal/markdown/markdown.go` / `markdown_test.go` — `ToBlocks` walker.
- `internal/cli/body.go` / `body_test.go` — `--body-file` loading + output shaping.

**Modify:**
- `internal/notion/errors.go` — add `ErrAmbiguousWrite`.
- `internal/service/service.go` — `BodyRequest`, `BodyResult`, `BodyWriteError`, `replaceBody`, new `body` param on `Upsert`/`Set`/`SetByID`; update `service_test.go` call sites.
- `internal/cli/upsert.go`, `internal/cli/set.go` — `--body-file` flag + shared output path.
- `internal/cli/output.go` — `exitCodeFor` handles `*service.BodyWriteError` (exit 1).
- `README.md`, `README.it.md`, `skills/notion-track/SKILL.md` — docs.
- `go.mod` / `go.sum` — goldmark.

---

## Task 1: `notion.Block` and `Span` with `MarshalJSON`

**Files:**
- Create: `internal/notion/block.go`
- Test: `internal/notion/block_test.go`

**Interfaces:**
- Produces: `notion.Block{Type string; RichText []Span; Checked bool; Language string; Children []Block}`; `notion.Span{Content, Link string; Bold, Italic, Code, Strikethrough bool}`. `Block` implements `json.Marshaler`, emitting Notion's `{"type":X,"X":{…}}` append shape.

- [ ] **Step 1: Write the failing test** (`internal/notion/block_test.go`)

Compare structurally (Go marshals map keys sorted, so assert on the decoded shape, not a golden string):

```go
package notion

import (
	"encoding/json"
	"reflect"
	"testing"
)

func marshalToMap(t *testing.T, b Block) map[string]any {
	t.Helper()
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func TestMarshalParagraphWithAnnotatedSpans(t *testing.T) {
	b := Block{Type: "paragraph", RichText: []Span{
		{Content: "plain "},
		{Content: "bold", Bold: true},
		{Content: "link", Link: "https://x.test"},
	}}
	m := marshalToMap(t, b)
	if m["type"] != "paragraph" {
		t.Fatalf("type = %v", m["type"])
	}
	para := m["paragraph"].(map[string]any)
	rt := para["rich_text"].([]any)
	if len(rt) != 3 {
		t.Fatalf("want 3 spans, got %d", len(rt))
	}
	first := rt[0].(map[string]any)
	if first["type"] != "text" {
		t.Fatalf("span type = %v", first["type"])
	}
	if first["text"].(map[string]any)["content"] != "plain " {
		t.Fatalf("content = %v", first["text"])
	}
	if _, hasAnn := first["annotations"]; hasAnn {
		t.Fatal("plain span must not carry annotations")
	}
	second := rt[1].(map[string]any)
	if second["annotations"].(map[string]any)["bold"] != true {
		t.Fatalf("bold span missing annotation: %v", second)
	}
	third := rt[2].(map[string]any)
	if third["text"].(map[string]any)["link"].(map[string]any)["url"] != "https://x.test" {
		t.Fatalf("link span missing url: %v", third)
	}
}

func TestMarshalDividerHasEmptyBody(t *testing.T) {
	m := marshalToMap(t, Block{Type: "divider"})
	body := m["divider"].(map[string]any)
	if len(body) != 0 {
		t.Fatalf("divider body should be empty, got %v", body)
	}
	if _, ok := body["rich_text"]; ok {
		t.Fatal("divider must not carry rich_text")
	}
}

func TestMarshalToDoAndCodeAndChildren(t *testing.T) {
	todo := marshalToMap(t, Block{Type: "to_do", RichText: []Span{{Content: "x"}}, Checked: true})
	if todo["to_do"].(map[string]any)["checked"] != true {
		t.Fatalf("checked missing: %v", todo)
	}
	code := marshalToMap(t, Block{Type: "code", RichText: []Span{{Content: "fmt.Println()"}}, Language: "go"})
	if code["code"].(map[string]any)["language"] != "go" {
		t.Fatalf("language missing: %v", code)
	}
	nested := marshalToMap(t, Block{
		Type:     "bulleted_list_item",
		RichText: []Span{{Content: "parent"}},
		Children: []Block{{Type: "bulleted_list_item", RichText: []Span{{Content: "child"}}}},
	})
	kids := nested["bulleted_list_item"].(map[string]any)["children"].([]any)
	if len(kids) != 1 {
		t.Fatalf("want 1 child, got %v", kids)
	}
	_ = reflect.DeepEqual // keep import if trimmed
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notion/ -run TestMarshal -v`
Expected: FAIL — `undefined: Block` (field set differs from existing types) / compile error.

- [ ] **Step 3: Write the implementation** (`internal/notion/block.go`)

```go
package notion

import "encoding/json"

// Block is one Notion block in the shape internal/markdown builds and the
// client serializes. MarshalJSON emits the nested {"type":X,"X":{…}} form the
// append endpoint expects. It is a pure data type: it performs no I/O and does
// not depend on the HTTP client, so internal/markdown can import it the same
// way internal/tracker already imports Schema.
type Block struct {
	Type     string // paragraph, heading_1..3, bulleted_list_item,
	// numbered_list_item, to_do, code, quote, divider
	RichText []Span
	Checked  bool    // to_do only
	Language string  // code only
	Children []Block // nested list/quote children; ≤2 levels materialized
}

// Span is a writable rich-text fragment with its annotations. It is kept
// separate from the read-oriented RichText/Text types in types.go so the read
// path stays untouched.
type Span struct {
	Content       string
	Link          string // url; "" when none
	Bold          bool
	Italic        bool
	Code          bool
	Strikethrough bool
}

func (s Span) toJSON() map[string]any {
	text := map[string]any{"content": s.Content}
	if s.Link != "" {
		text["link"] = map[string]string{"url": s.Link}
	}
	out := map[string]any{"type": "text", "text": text}
	ann := map[string]bool{}
	if s.Bold {
		ann["bold"] = true
	}
	if s.Italic {
		ann["italic"] = true
	}
	if s.Code {
		ann["code"] = true
	}
	if s.Strikethrough {
		ann["strikethrough"] = true
	}
	// Only attach annotations when at least one is set; Notion fills defaults.
	if len(ann) > 0 {
		out["annotations"] = ann
	}
	return out
}

// MarshalJSON emits the Notion append shape for a block.
func (b Block) MarshalJSON() ([]byte, error) {
	inner := map[string]any{}
	if b.Type != "divider" {
		spans := make([]map[string]any, 0, len(b.RichText))
		for _, s := range b.RichText {
			spans = append(spans, s.toJSON())
		}
		inner["rich_text"] = spans
	}
	if b.Type == "to_do" {
		inner["checked"] = b.Checked
	}
	if b.Type == "code" {
		inner["language"] = b.Language
	}
	if len(b.Children) > 0 {
		inner["children"] = b.Children
	}
	return json.Marshal(map[string]any{"type": b.Type, b.Type: inner})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notion/ -run TestMarshal -v`
Expected: PASS. Then `go vet ./internal/notion/` clean (remove the `reflect` import from the test if unused).

- [ ] **Step 5: Commit**

```bash
git add internal/notion/block.go internal/notion/block_test.go
git commit -m "feat(notion): Block and Span types with Notion append MarshalJSON"
```

---

## Task 2: request grouping + pre-flight validation (pure)

**Files:**
- Create: `internal/notion/blocks.go` (this task adds only the pure helpers; network methods come in Task 4)
- Test: `internal/notion/blocks_test.go`

**Interfaces:**
- Consumes: `notion.Block` (Task 1).
- Produces: `ValidateAppendable(blocks []Block) error`; `splitIntoRequests(blocks []Block) [][]Block`; unexported `countBlocks(Block) int`, `blockBytes(Block) int`; consts `maxChildrenPerRequest=100`, `maxBlocksPerRequest=1000`, `maxBytesPerRequest=450<<10`.

- [ ] **Step 1: Write the failing test** (`internal/notion/blocks_test.go`)

```go
package notion

import "testing"

func para(n int) Block { // a paragraph counted as 1 block, small bytes
	return Block{Type: "paragraph", RichText: []Span{{Content: "x"}}}
}

func TestValidateAppendableRejectsTooManyDirectChildren(t *testing.T) {
	kids := make([]Block, 101)
	for i := range kids {
		kids[i] = para(0)
	}
	b := Block{Type: "bulleted_list_item", RichText: []Span{{Content: "p"}}, Children: kids}
	if err := ValidateAppendable([]Block{b}); err == nil {
		t.Fatal("want error for >100 direct children, got nil")
	}
}

func TestValidateAppendableAcceptsTwoLevelsRejectsThree(t *testing.T) {
	twoLevel := Block{Type: "bulleted_list_item", RichText: []Span{{Content: "a"}},
		Children: []Block{{Type: "bulleted_list_item", RichText: []Span{{Content: "b"}}}}}
	if err := ValidateAppendable([]Block{twoLevel}); err != nil {
		t.Fatalf("2 levels must be accepted: %v", err)
	}
	threeLevel := Block{Type: "bulleted_list_item", RichText: []Span{{Content: "a"}},
		Children: []Block{{Type: "bulleted_list_item", RichText: []Span{{Content: "b"}},
			Children: []Block{{Type: "bulleted_list_item", RichText: []Span{{Content: "c"}}}}}}}
	if err := ValidateAppendable([]Block{threeLevel}); err == nil {
		t.Fatal("3 levels must be rejected pre-flight")
	}
}

func TestValidateAppendableAcceptsNormalDoc(t *testing.T) {
	blocks := []Block{para(0), para(0), {Type: "divider"}}
	if err := ValidateAppendable(blocks); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSplitIntoRequestsChunksByTopLevelCount(t *testing.T) {
	blocks := make([]Block, 250)
	for i := range blocks {
		blocks[i] = para(0)
	}
	batches := splitIntoRequests(blocks)
	if len(batches) != 3 {
		t.Fatalf("want 3 batches (100+100+50), got %d", len(batches))
	}
	if len(batches[0]) != 100 || len(batches[2]) != 50 {
		t.Fatalf("batch sizes = %d,%d,%d", len(batches[0]), len(batches[1]), len(batches[2]))
	}
	// Order preserved and no block dropped.
	total := 0
	for _, b := range batches {
		total += len(b)
	}
	if total != 250 {
		t.Fatalf("lost blocks: total = %d", total)
	}
}

func TestSplitIntoRequestsCountsNestedBlocksTowardTotal(t *testing.T) {
	// One top-level item with 999 children = 1000 blocks: fills a batch alone.
	kids := make([]Block, 99)
	for i := range kids {
		kids[i] = para(0)
	}
	big := Block{Type: "bulleted_list_item", RichText: []Span{{Content: "p"}}, Children: kids} // 100 blocks
	blocks := []Block{big, big, big, big, big, big, big, big, big, big, big} // 11×100 = 1100 blocks
	batches := splitIntoRequests(blocks)
	if len(batches) < 2 {
		t.Fatalf("1100 nested blocks must span >1 batch by the 1000-block cap, got %d", len(batches))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notion/ -run 'TestValidate|TestSplit' -v`
Expected: FAIL — `undefined: ValidateAppendable` / `undefined: splitIntoRequests`.

- [ ] **Step 3: Write the implementation** (`internal/notion/blocks.go`)

```go
package notion

import (
	"encoding/json"
	"fmt"
)

// Per-request Notion limits for the block-children endpoints. maxBytesPerRequest
// keeps a margin under Notion's real 500KB payload cap for JSON overhead.
const (
	maxChildrenPerRequest = 100
	maxBlocksPerRequest   = 1000
	maxBytesPerRequest    = 450 << 10
)

// countBlocks returns the number of blocks in a subtree (the block plus every
// descendant), which is what Notion's per-payload block cap counts.
func countBlocks(b Block) int {
	n := 1
	for _, c := range b.Children {
		n += countBlocks(c)
	}
	return n
}

// blockBytes returns the serialized size of a block for the byte budget. An
// unserializable block is treated as over budget so it is rejected, not
// silently sent.
func blockBytes(b Block) int {
	data, err := json.Marshal(b)
	if err != nil {
		return maxBytesPerRequest + 1
	}
	return len(data)
}

// blockDepth returns the nesting depth of a subtree: a leaf is 1, a block whose
// children are leaves is 2, and so on. Notion accepts at most 2 levels per
// append request.
func blockDepth(b Block) int {
	d := 1
	for _, c := range b.Children {
		if cd := 1 + blockDepth(c); cd > d {
			d = cd
		}
	}
	return d
}

// ValidateAppendable reports whether blocks can be materialized as valid append
// requests WITHOUT any network call. It fails only on an irreducible case: a
// single top-level element that cannot fit one request alone (>100 direct
// children, a subtree over the block/byte caps, or nesting deeper than 2
// levels). Grouping small blocks is splitIntoRequests' job; deeper nesting than
// v1 supports surfaces here as a clear pre-flight error rather than a
// mid-replace 400. The depth check is defense in depth: the walker already
// promotes deep nesting, so a positive here means a walker bug, not user input.
func ValidateAppendable(blocks []Block) error {
	for i, b := range blocks {
		if len(b.Children) > maxChildrenPerRequest {
			return fmt.Errorf(
				"block %d (%s) has %d direct children, over the %d-per-request limit; deeply nested content is not supported yet",
				i, b.Type, len(b.Children), maxChildrenPerRequest)
		}
		if d := blockDepth(b); d > 2 {
			return fmt.Errorf("block %d (%s) nests %d levels deep, over Notion's 2-level per-request limit",
				i, b.Type, d)
		}
		if n := countBlocks(b); n > maxBlocksPerRequest {
			return fmt.Errorf("block %d (%s) expands to %d blocks, over the %d-per-request limit",
				i, b.Type, n, maxBlocksPerRequest)
		}
		if sz := blockBytes(b); sz > maxBytesPerRequest {
			return fmt.Errorf("block %d (%s) serializes to %d bytes, over the %d-byte per-request limit",
				i, b.Type, sz, maxBytesPerRequest)
		}
	}
	return nil
}

// splitIntoRequests groups top-level blocks into request-sized batches, each
// within all three per-request limits. It assumes ValidateAppendable has
// already rejected any single block too big to fit a request alone.
func splitIntoRequests(blocks []Block) [][]Block {
	var batches [][]Block
	var cur []Block
	curBlocks, curBytes := 0, 0
	flush := func() {
		if len(cur) > 0 {
			batches = append(batches, cur)
			cur, curBlocks, curBytes = nil, 0, 0
		}
	}
	for _, b := range blocks {
		n, sz := countBlocks(b), blockBytes(b)
		over := len(cur)+1 > maxChildrenPerRequest ||
			curBlocks+n > maxBlocksPerRequest ||
			curBytes+sz > maxBytesPerRequest
		if len(cur) > 0 && over {
			flush()
		}
		cur = append(cur, b)
		curBlocks += n
		curBytes += sz
	}
	flush()
	return batches
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notion/ -run 'TestValidate|TestSplit' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/notion/blocks.go internal/notion/blocks_test.go
git commit -m "feat(notion): pre-flight ValidateAppendable and request chunking"
```

---

## Task 3: third retry mode — `doRejectRetryable` + `ErrAmbiguousWrite`

**Files:**
- Modify: `internal/notion/errors.go` (add sentinel)
- Modify: `internal/notion/blocks.go` (add `rejectedByServer`, `doRejectRetryable`)
- Test: `internal/notion/blocks_test.go` (append cases)

**Interfaces:**
- Consumes: existing `doOnce`, `wait`, `backoffFor`, `APIError`, `statusServiceOverload` (=529, already in retry.go).
- Produces: `ErrAmbiguousWrite` sentinel; `rejectedByServer(status int) bool` (={429,503,529}); `(c *Client) doRejectRetryable(ctx, method, path string, body, out any) error`.

- [ ] **Step 1: Write the failing test** (append to `internal/notion/blocks_test.go`)

```go
import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"
)

func testClient(url string) *Client {
	return New("tok", WithBaseURL(url), WithSleep(func(time.Duration) {}))
}

func TestDoRejectRetryableRetriesOn429ThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"code":"rate_limited","message":"slow down"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := testClient(srv.URL).doRejectRetryable(context.Background(), http.MethodPatch, "/x", map[string]any{}, nil)
	if err != nil {
		t.Fatalf("want success after retry, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("want 2 calls (429 then ok), got %d", calls)
	}
}

func TestDoRejectRetryableTreats504AsAmbiguousWithoutRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer srv.Close()

	err := testClient(srv.URL).doRejectRetryable(context.Background(), http.MethodPatch, "/x", map[string]any{}, nil)
	if !errors.Is(err, ErrAmbiguousWrite) {
		t.Fatalf("504 must be ambiguous, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("ambiguous status must NOT be retried, got %d calls", calls)
	}
}

func TestDoRejectRetryableReturns400AsIsNotAmbiguous(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"code":"validation_error","message":"bad block"}`))
	}))
	defer srv.Close()

	err := testClient(srv.URL).doRejectRetryable(context.Background(), http.MethodPatch, "/x", map[string]any{}, nil)
	if errors.Is(err, ErrAmbiguousWrite) {
		t.Fatal("400 is a rejected client error, must not be ambiguous")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Fatalf("want APIError 400, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notion/ -run TestDoRejectRetryable -v`
Expected: FAIL — `undefined: ErrAmbiguousWrite` / `doRejectRetryable`.

- [ ] **Step 3: Write the implementation**

Add to `internal/notion/errors.go` var block:

```go
	// ErrAmbiguousWrite marks a non-idempotent write whose outcome is unknown:
	// a transport error or a 500/502/504 that may have been applied before the
	// failure. Callers surface it as "re-run to converge" rather than retrying
	// automatically, which could duplicate.
	ErrAmbiguousWrite = errors.New("notion: write outcome unknown; re-run to converge")
```

Add to `internal/notion/blocks.go`:

```go
import (
	"context"
	"errors"
	"net/http"
)

// rejectedByServer reports statuses where Notion certainly refused the request
// WITHOUT processing it, making a retry safe even for a non-idempotent write:
// 429 (rate limited), 503 (service unavailable), 529 (service overload). It
// deliberately excludes 500/502/504 and transport errors, which are ambiguous.
func rejectedByServer(status int) bool {
	return status == http.StatusTooManyRequests ||
		status == http.StatusServiceUnavailable ||
		status == statusServiceOverload
}

// doRejectRetryable performs a non-idempotent request, retrying ONLY on
// rejectedByServer statuses (honoring Retry-After like do). A 4xx is returned
// as-is (rejected, no side effect). A 5xx outside the safe set, or a
// transport error, is joined with ErrAmbiguousWrite so the caller can tell the
// user the write may be half-applied and to re-run.
func (c *Client) doRejectRetryable(ctx context.Context, method, path string, body, out any) error {
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		err := c.doOnce(ctx, method, path, body, out)
		if err == nil {
			return nil
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			switch {
			case rejectedByServer(apiErr.Status):
				if attempt == c.maxRetries {
					return err // exhausted; rejected each time → nothing applied
				}
				if werr := c.wait(ctx, backoffFor(attempt, apiErr.RetryAfter)); werr != nil {
					return werr
				}
				continue
			case apiErr.Status >= 500:
				// 500/502/504: may have reached Notion and been applied.
				return fmt.Errorf("%w: %w", ErrAmbiguousWrite, err)
			default:
				return err // 4xx: rejected, no side effect
			}
		}
		// Transport-level error (timeout, reset): ambiguous. %w twice keeps both
		// ErrAmbiguousWrite and the underlying error reachable via errors.Is/As.
		return fmt.Errorf("%w: %w", ErrAmbiguousWrite, err)
	}
	return nil // unreachable: the loop returns on the last attempt
}
```

Note: `blocks.go` already imports `encoding/json` and `fmt` from Task 2; add `context`, `errors`, `net/http`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notion/ -run TestDoRejectRetryable -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/notion/errors.go internal/notion/blocks.go internal/notion/blocks_test.go
git commit -m "feat(notion): reject-only retry mode with ErrAmbiguousWrite for non-idempotent writes"
```

---

## Task 4: block endpoints — list / append / delete

**Files:**
- Modify: `internal/notion/blocks.go`
- Test: `internal/notion/blocks_test.go`

**Interfaces:**
- Consumes: `do` (existing), `doRejectRetryable` (Task 3), `splitIntoRequests`/`ValidateAppendable` (Task 2), `ErrNotFound` (existing).
- Produces: `ChildBlock{ID, Type string}`; `(c *Client) ListBlockChildren(ctx, blockID string) ([]ChildBlock, error)`; `(c *Client) AppendBlockChildren(ctx, blockID string, blocks []Block) error`; `(c *Client) DeleteBlock(ctx, blockID string) error`.

- [ ] **Step 1: Write the failing test**

```go
// This test file already imports context/errors/net/http/httptest/atomic/time
// from Task 3; the append test additionally needs "encoding/json" for the body
// decoder. "net/url" belongs in blocks.go (production), NOT in this test.
import "encoding/json"

func TestAppendBlockChildrenChunksSequentiallyWithPositionEnd(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			bodies = append(bodies, b)
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	blocks := make([]Block, 150)
	for i := range blocks {
		blocks[i] = Block{Type: "paragraph", RichText: []Span{{Content: "x"}}}
	}
	if err := testClient(srv.URL).AppendBlockChildren(context.Background(), "page1", blocks); err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("150 blocks want 2 requests (100+50), got %d", len(bodies))
	}
	for _, b := range bodies {
		if pos, ok := b["position"].(map[string]any); !ok || pos["type"] != "end" {
			t.Fatalf("append must set position end, got %v", b["position"])
		}
	}
	if n := len(bodies[0]["children"].([]any)); n != 100 {
		t.Fatalf("first batch = %d, want 100", n)
	}
}

func TestAppendBlockChildrenValidatesBeforeAnyRequest(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()

	kids := make([]Block, 101)
	for i := range kids {
		kids[i] = Block{Type: "paragraph", RichText: []Span{{Content: "x"}}}
	}
	bad := []Block{{Type: "bulleted_list_item", RichText: []Span{{Content: "p"}}, Children: kids}}
	if err := testClient(srv.URL).AppendBlockChildren(context.Background(), "page1", bad); err == nil {
		t.Fatal("want validation error")
	}
	if called {
		t.Fatal("no request may be sent when validation fails")
	}
}

func TestListBlockChildrenPaginatesPastOneHundred(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start_cursor") == "" {
			w.Write([]byte(`{"results":[{"id":"a","type":"paragraph"}],"has_more":true,"next_cursor":"c2"}`))
			return
		}
		w.Write([]byte(`{"results":[{"id":"b","type":"child_page"}],"has_more":false}`))
	}))
	defer srv.Close()

	got, err := testClient(srv.URL).ListBlockChildren(context.Background(), "page1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0].ID != "a" || got[1].Type != "child_page" {
		t.Fatalf("pagination lost content: %+v", got)
	}
}

func TestDeleteBlockTreats404AsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":"object_not_found","message":"gone"}`))
	}))
	defer srv.Close()
	if err := testClient(srv.URL).DeleteBlock(context.Background(), "b1"); err != nil {
		t.Fatalf("404 on delete must be success, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notion/ -run 'TestAppend|TestList|TestDelete' -v`
Expected: FAIL — `undefined: AppendBlockChildren` etc.

- [ ] **Step 3: Write the implementation** (append to `internal/notion/blocks.go`)

```go
// ChildBlock is a direct child of a page, flattened to what replace needs: its
// id (to delete) and its type (to skip child_page/child_database).
type ChildBlock struct {
	ID   string
	Type string
}

// ListBlockChildren returns the direct children of blockID, following
// pagination to has_more=false. GET is idempotent → do.
func (c *Client) ListBlockChildren(ctx context.Context, blockID string) ([]ChildBlock, error) {
	var out []ChildBlock
	cursor := ""
	for {
		var resp struct {
			Results []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"results"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		}
		path := "/v1/blocks/" + url.PathEscape(blockID) + "/children?page_size=100"
		if cursor != "" {
			path += "&start_cursor=" + url.QueryEscape(cursor)
		}
		if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Results {
			out = append(out, ChildBlock{ID: r.ID, Type: r.Type})
		}
		if !resp.HasMore || resp.NextCursor == "" {
			return out, nil
		}
		if resp.NextCursor == cursor {
			return nil, fmt.Errorf("notion: block children pagination stalled, cursor %q repeated", resp.NextCursor)
		}
		cursor = resp.NextCursor
	}
}

// AppendBlockChildren appends blocks as children of blockID, in document order.
// It validates the whole slice first and returns before any network I/O if a
// single element cannot fit one request. It then PATCHes each request-sized
// batch with position end, STRICTLY SEQUENTIALLY — order holds only because
// each batch lands after the previous one. Each batch uses doRejectRetryable.
func (c *Client) AppendBlockChildren(ctx context.Context, blockID string, blocks []Block) error {
	if err := ValidateAppendable(blocks); err != nil {
		return err
	}
	path := "/v1/blocks/" + url.PathEscape(blockID) + "/children"
	for _, batch := range splitIntoRequests(blocks) {
		body := map[string]any{
			"children": batch,
			"position": map[string]string{"type": "end"},
		}
		if err := c.doRejectRetryable(ctx, http.MethodPatch, path, body, nil); err != nil {
			return err
		}
	}
	return nil
}

// DeleteBlock archives a block. A 404 means the block is already gone, which
// satisfies the goal, so it is success; every other error propagates. DELETE
// is idempotent → do.
func (c *Client) DeleteBlock(ctx context.Context, blockID string) error {
	path := "/v1/blocks/" + url.PathEscape(blockID)
	err := c.do(ctx, http.MethodDelete, path, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
```

Add `net/url` to the `blocks.go` imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notion/ -race -v`
Expected: PASS (all notion tests, old and new).

- [ ] **Step 5: Commit**

```bash
git add internal/notion/blocks.go internal/notion/blocks_test.go
git commit -m "feat(notion): ListBlockChildren, AppendBlockChildren, DeleteBlock"
```

---

## Task 5: markdown pure helpers — language map + content splitting

**Files:**
- Create: `internal/markdown/language.go`, `internal/markdown/language_test.go`
- Create: `internal/markdown/split.go`, `internal/markdown/split_test.go`

**Interfaces:**
- Consumes: `notion.Span` (Task 1).
- Produces: `CanonicalLanguage(raw string) string`; `splitLongSpans(spans []notion.Span) []notion.Span`; `splitBlockOnSpanLimit(b notion.Block) []notion.Block`; consts `maxRichTextChars=2000`, `maxSpansPerBlock=100`.

- [ ] **Step 1: Write the failing tests**

`internal/markdown/language_test.go`:

```go
package markdown

import "testing"

func TestCanonicalLanguage(t *testing.T) {
	cases := map[string]string{
		"js": "javascript", "TS": "typescript", "py": "python", "sh": "shell",
		"golang": "go", "go": "go", "yaml": "yaml", "": "plain text",
		"klingon": "plain text", "  Python ": "python",
	}
	for in, want := range cases {
		if got := CanonicalLanguage(in); got != want {
			t.Errorf("CanonicalLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}
```

`internal/markdown/split_test.go`:

```go
package markdown

import (
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

func TestSplitLongSpansBreaksAtWordBoundaryAndKeepsAnnotations(t *testing.T) {
	long := strings.Repeat("word ", 500) // 2500 chars
	out := splitLongSpans([]notion.Span{{Content: long, Bold: true}})
	if len(out) < 2 {
		t.Fatalf("2500-char span must split, got %d", len(out))
	}
	for _, s := range out {
		if len([]rune(s.Content)) > maxRichTextChars {
			t.Fatalf("fragment over limit: %d runes", len([]rune(s.Content)))
		}
		if !s.Bold {
			t.Fatal("fragment lost the bold annotation")
		}
	}
	if !strings.HasSuffix(out[0].Content, " ") && !strings.HasSuffix(out[0].Content, "word") {
		t.Fatalf("first fragment should end on a word boundary: %q", out[0].Content[len(out[0].Content)-10:])
	}
}

func TestSplitLongSpansSplitsUnbrokenRun(t *testing.T) {
	long := strings.Repeat("x", 5000) // no spaces
	out := splitLongSpans([]notion.Span{{Content: long}})
	if len(out) != 3 { // 2000+2000+1000
		t.Fatalf("want 3 fragments, got %d", len(out))
	}
}

func TestSplitBlockOnSpanLimit(t *testing.T) {
	spans := make([]notion.Span, 250)
	for i := range spans {
		spans[i] = notion.Span{Content: "s"}
	}
	kid := notion.Block{Type: "bulleted_list_item", RichText: []notion.Span{{Content: "k"}}}
	blocks := splitBlockOnSpanLimit(notion.Block{Type: "paragraph", RichText: spans, Children: []notion.Block{kid}})
	if len(blocks) != 3 {
		t.Fatalf("250 spans want 3 blocks, got %d", len(blocks))
	}
	if len(blocks[0].Children) != 0 || len(blocks[1].Children) != 0 {
		t.Fatal("children must not be on the non-final fragments")
	}
	if len(blocks[len(blocks)-1].Children) != 1 {
		t.Fatal("children must move to the last fragment (text reads before nested content)")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/markdown/ -v`
Expected: FAIL — package/functions undefined.

- [ ] **Step 3: Write the implementations**

`internal/markdown/language.go`:

```go
// Package markdown converts Markdown into Notion blocks. It is pure domain: it
// performs no network I/O, so it is table-tested without a server.
package markdown

import "strings"

// notionLanguages is the set of code-block languages Notion's API accepts. A
// value outside it makes the append 400, so anything unknown falls back to
// "plain text". Maintained subset of Notion's enum: common languages plus what
// a task body realistically carries.
var notionLanguages = map[string]bool{
	"plain text": true, "bash": true, "c": true, "c++": true, "c#": true,
	"css": true, "diff": true, "docker": true, "go": true, "graphql": true,
	"html": true, "java": true, "javascript": true, "json": true, "kotlin": true,
	"lua": true, "makefile": true, "markdown": true, "objective-c": true,
	"perl": true, "php": true, "powershell": true, "python": true, "r": true,
	"ruby": true, "rust": true, "scala": true, "shell": true, "sql": true,
	"swift": true, "toml": true, "typescript": true, "xml": true, "yaml": true,
}

// languageAliases maps common fence tags not already canonical onto Notion's
// name. Tags already present in notionLanguages (e.g. "bash") are not aliased.
var languageAliases = map[string]string{
	"js": "javascript", "ts": "typescript", "py": "python", "rb": "ruby",
	"sh": "shell", "zsh": "shell", "golang": "go", "yml": "yaml",
	"md": "markdown", "dockerfile": "docker", "cpp": "c++", "cs": "c#",
	"objc": "objective-c", "ps1": "powershell", "text": "plain text",
	"txt": "plain text", "": "plain text",
}

// CanonicalLanguage resolves a fence tag to a Notion-accepted language, applying
// aliases and falling back to "plain text" for anything unknown, so the append
// can never 400 on an invalid language.
func CanonicalLanguage(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if canon, ok := languageAliases[s]; ok {
		s = canon
	}
	if notionLanguages[s] {
		return s
	}
	return "plain text"
}
```

`internal/markdown/split.go`:

```go
package markdown

import "github.com/marcoarnulfo/notion-cli/internal/notion"

const (
	maxRichTextChars = 2000 // Notion's rich_text.text.content cap
	maxSpansPerBlock = 100  // Notion's per-array element cap
)

// splitLongSpans splits any span whose content exceeds maxRichTextChars into
// several spans, preferring a break at the last space at or before the limit so
// words are not cut mid-way. Every fragment keeps the original annotations and
// link. Counts runes, not bytes, so multibyte content is never cut mid-rune.
func splitLongSpans(spans []notion.Span) []notion.Span {
	var out []notion.Span
	for _, s := range spans {
		r := []rune(s.Content)
		if len(r) <= maxRichTextChars {
			out = append(out, s)
			continue
		}
		for len(r) > maxRichTextChars {
			cut := lastSpaceBefore(r, maxRichTextChars)
			frag := s
			frag.Content = string(r[:cut])
			out = append(out, frag)
			r = r[cut:]
		}
		if len(r) > 0 {
			frag := s
			frag.Content = string(r)
			out = append(out, frag)
		}
	}
	return out
}

// lastSpaceBefore returns the cut index: just after the last space at or before
// limit, or limit itself when the run has no space (a single long word still
// gets cut so no fragment exceeds the limit).
func lastSpaceBefore(r []rune, limit int) int {
	for i := limit; i > 0; i-- {
		if r[i-1] == ' ' {
			return i
		}
	}
	return limit
}

// splitBlockOnSpanLimit splits a block whose rich text exceeds maxSpansPerBlock
// into several blocks of the same type, each with at most maxSpansPerBlock
// spans. Children move to the LAST fragment, so the split text still reads
// before the nested content. Called after splitLongSpans, which is what can
// push a block's span count over the limit.
func splitBlockOnSpanLimit(b notion.Block) []notion.Block {
	if len(b.RichText) <= maxSpansPerBlock {
		return []notion.Block{b}
	}
	children := b.Children
	var frags []notion.Block
	spans := b.RichText
	for len(spans) > 0 {
		n := maxSpansPerBlock
		if n > len(spans) {
			n = len(spans)
		}
		frag := b
		frag.RichText = spans[:n]
		frag.Children = nil
		frags = append(frags, frag)
		spans = spans[n:]
	}
	frags[len(frags)-1].Children = children // children read after all the text
	return frags
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/markdown/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/markdown/language.go internal/markdown/language_test.go internal/markdown/split.go internal/markdown/split_test.go
git commit -m "feat(markdown): language whitelist and rich-text/span splitting"
```

---

## Task 6: `ToBlocks` walker — block structure (plain text)

Adds the goldmark dependency and the AST→blocks walker producing the full block structure with **plain-text** rich text (inline formatting comes in Task 7). Delivers working `--body-file` for plain bodies once wired.

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/markdown/markdown.go`, `internal/markdown/markdown_test.go`

**Interfaces:**
- Consumes: `notion.Block`/`notion.Span` (Task 1), `CanonicalLanguage`, `splitLongSpans`, `splitBlockOnSpanLimit` (Task 5).
- Produces: `ToBlocks(src []byte) (blocks []notion.Block, warnings []string, err error)`.

- [ ] **Step 1: Add goldmark**

```bash
go get github.com/yuin/goldmark@latest
```
Expected: `go.mod` gains `github.com/yuin/goldmark vX.Y.Z` (v1.7+), `go.sum` updated.

- [ ] **Step 2: Write the failing test** (`internal/markdown/markdown_test.go`)

```go
package markdown

import (
	"strings"
	"testing"
)

func blockTypes(src string) ([]string, []string) {
	blocks, warnings, _ := ToBlocks([]byte(src))
	var types []string
	for _, b := range blocks {
		types = append(types, b.Type)
	}
	return types, warnings
}

func TestToBlocksMapsCoreConstructs(t *testing.T) {
	src := "# H1\n\n## H2\n\ntext\n\n- a\n- b\n\n1. one\n\n- [ ] todo\n- [x] done\n\n> quote\n\n---\n\n```go\nfmt.Println()\n```\n"
	types, _ := blockTypes(src)
	joined := strings.Join(types, ",")
	for _, want := range []string{"heading_1", "heading_2", "paragraph", "bulleted_list_item", "numbered_list_item", "to_do", "quote", "divider", "code"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %s", want, joined)
		}
	}
}

func TestToBlocksTaskCheckboxState(t *testing.T) {
	blocks, _, _ := ToBlocks([]byte("- [ ] open\n- [x] closed\n"))
	var todos []bool
	for _, b := range blocks {
		if b.Type == "to_do" {
			todos = append(todos, b.Checked)
		}
	}
	if len(todos) != 2 || todos[0] != false || todos[1] != true {
		t.Fatalf("checkbox states = %v, want [false true]", todos)
	}
}

func TestToBlocksHeadingDeeperThanThreeClampsToH3(t *testing.T) {
	types, _ := blockTypes("#### four\n\n##### five\n")
	for _, ty := range types {
		if ty != "heading_3" {
			t.Fatalf("h4/h5 must clamp to heading_3, got %s", ty)
		}
	}
}

func TestToBlocksCodeLanguageCanonicalized(t *testing.T) {
	blocks, _, _ := ToBlocks([]byte("```js\nx=1\n```\n"))
	if blocks[0].Type != "code" || blocks[0].Language != "javascript" {
		t.Fatalf("code language = %q", blocks[0].Language)
	}
	if got := blocks[0].RichText[0].Content; !strings.Contains(got, "x=1") {
		t.Fatalf("code content lost: %q", got)
	}
}

func TestToBlocksNestedListKeepsTwoLevelsAndPromotesDeeperInOrder(t *testing.T) {
	src := "- a\n  - b\n    - c\n"
	blocks, warnings, _ := ToBlocks([]byte(src))
	// Top level: item "a" with children; "c" (level 3) is promoted to level 2,
	// as a sibling that FOLLOWS "b" (reading order), never precedes it.
	if blocks[0].Type != "bulleted_list_item" || len(blocks[0].Children) == 0 {
		t.Fatalf("level-2 nesting not materialized: %+v", blocks[0])
	}
	kids := blocks[0].Children
	bi := indexMentioning(kids, "b")
	ci := indexMentioning(kids, "c")
	if bi < 0 || ci < 0 {
		t.Fatalf("both 'b' and 'c' must survive as children: %+v", kids)
	}
	if bi > ci {
		t.Fatalf("promoted 'c' (@%d) must follow its parent 'b' (@%d): %+v", ci, bi, kids)
	}
	if len(kids[bi].Children) != 0 {
		t.Fatal("level 3 must not be materialized under 'b' (2-level cap)")
	}
	if !hasWarning(warnings, "nesting") {
		t.Fatalf("deep nesting must warn, got %v", warnings)
	}
}

func TestToBlocksTableDegradesToCodeWithWarning(t *testing.T) {
	src := "| a | b |\n|---|---|\n| 1 | 2 |\n"
	blocks, warnings, _ := ToBlocks([]byte(src))
	if len(blocks) == 0 || blocks[0].Type != "code" {
		t.Fatalf("table should degrade to a code block, got %+v", blocks)
	}
	if !hasWarning(warnings, "table") {
		t.Fatalf("table degradation must warn, got %v", warnings)
	}
}

func TestToBlocksSplitsCodeFenceOver2000Chars(t *testing.T) {
	// A 3000-char code fence must split so no span exceeds the 2000-char cap —
	// otherwise the append 400s mid-replace, which the pre-flight cannot catch.
	huge := "```\n" + strings.Repeat("x", 3000) + "\n```\n"
	blocks, _, _ := ToBlocks([]byte(huge))
	seenCode := false
	for _, b := range blocks {
		if b.Type == "code" {
			seenCode = true
		}
		for _, s := range b.RichText {
			if len([]rune(s.Content)) > 2000 {
				t.Fatalf("a %s span is %d runes, over the 2000 cap", b.Type, len([]rune(s.Content)))
			}
		}
	}
	if !seenCode {
		t.Fatal("expected at least one code block")
	}
}

func TestToBlocksImageDegradesToLinkWithWarning(t *testing.T) {
	blocks, warnings, _ := ToBlocks([]byte("![alt](https://img.test/x.png)\n"))
	if !hasWarning(warnings, "image") {
		t.Fatalf("image must warn, got %v", warnings)
	}
	found := false
	for _, b := range blocks {
		for _, s := range b.RichText {
			if s.Link == "https://img.test/x.png" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("image must survive as a link span: %+v", blocks)
	}
}

func TestToBlocksBlockquoteMultiParagraphNoDuplication(t *testing.T) {
	// First paragraph is the quote's own text; the second becomes a child. The
	// first must NOT also appear as a child (the old two-pass bug).
	blocks, _, _ := ToBlocks([]byte("> first\n>\n> second\n"))
	if blocks[0].Type != "quote" {
		t.Fatalf("want quote, got %s", blocks[0].Type)
	}
	if got := spanText(blocks[0].RichText); !strings.Contains(got, "first") {
		t.Fatalf("quote text should be the first paragraph: %q", got)
	}
	for _, child := range blocks[0].Children {
		if strings.Contains(spanText(child.RichText), "first") {
			t.Fatal("first paragraph must not be duplicated as a child")
		}
	}
}

func TestToBlocksNormalizesCRLFAndBOM(t *testing.T) {
	types, _ := blockTypes("﻿# Title\r\n\r\nbody\r\n")
	if len(types) < 2 || types[0] != "heading_1" {
		t.Fatalf("BOM/CRLF not normalized: %v", types)
	}
}

// helpers
func hasWarning(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(strings.ToLower(w), sub) {
			return true
		}
	}
	return false
}

func spanText(spans []notion.Span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.Content)
	}
	return b.String()
}

// indexMentioning returns the index of the first block whose own text (or any
// descendant's) contains sub, or -1.
func indexMentioning(blocks []notion.Block, sub string) int {
	var walk func(b notion.Block) bool
	walk = func(b notion.Block) bool {
		if strings.Contains(spanText(b.RichText), sub) {
			return true
		}
		for _, c := range b.Children {
			if walk(c) {
				return true
			}
		}
		return false
	}
	for i, b := range blocks {
		if walk(b) {
			return i
		}
	}
	return -1
}
```

Add `import "github.com/marcoarnulfo/notion-cli/internal/notion"` to the test.

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/markdown/ -run TestToBlocks -v`
Expected: FAIL — `undefined: ToBlocks`.

- [ ] **Step 4: Write the implementation** (`internal/markdown/markdown.go`)

Walker producing **plain-text** spans (inline formatting comes in Task 7). Two invariants make this correct: (1) every text-bearing block — headings, paragraphs, list items, **code, and degraded table/HTML/unknown blocks** — goes through `emit`, which applies BOTH the 2000-char span split and the 100-span block split, so nothing can 400 on size; (2) content nested deeper than `maxNestDepth` is **promoted after** its parent (never before), and `ValidateAppendable` (Task 2) rejects any residual depth-3 as defense in depth.

```go
package markdown

import (
	"fmt"
	"strings"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

const maxNestDepth = 2 // Notion's per-request nesting cap; deeper is promoted.

// ToBlocks parses Markdown (GFM) and returns top-level Notion blocks, with
// nested list/quote content in each block's Children (≤2 levels). warnings
// describes every graceful degradation for the caller to print to stderr. err
// is reserved for inputs the mapper cannot represent at all (none today).
func ToBlocks(src []byte) ([]notion.Block, []string, error) {
	src = normalize(src)
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	doc := md.Parser().Parse(text.NewReader(src))

	c := &converter{src: src}
	var blocks []notion.Block
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		blocks = append(blocks, c.block(n, 1)...)
	}
	return blocks, c.warnings, nil
}

// normalize strips a leading BOM and converts CRLF/CR to LF before parsing.
// "﻿" is the BOM as an escape so it survives copy/paste of this plan.
func normalize(src []byte) []byte {
	s := strings.ReplaceAll(strings.ReplaceAll(string(src), "\r\n", "\n"), "\r", "\n")
	return []byte(strings.TrimPrefix(s, "﻿"))
}

type converter struct {
	src        []byte
	warnings   []string
	warnedNest bool
}

func (c *converter) warn(format string, args ...any) {
	c.warnings = append(c.warnings, fmt.Sprintf(format, args...))
}

func (c *converter) warnNesting() {
	if !c.warnedNest { // one warning, not one per over-deep node
		c.warn("nesting deeper than %d levels was flattened", maxNestDepth)
		c.warnedNest = true
	}
}

// emit finalizes a text-bearing block: it splits over-long spans (2000 chars)
// then over-long blocks (100 spans), so every produced block is within Notion's
// rich-text limits. EVERY text block must go through here — code and degraded
// constructs included, since those are the most likely to be large.
func (c *converter) emit(b notion.Block) []notion.Block {
	b.RichText = splitLongSpans(b.RichText)
	return splitBlockOnSpanLimit(b)
}

// block converts one block-level node into zero or more Notion blocks at the
// given nesting depth (1 = top level).
func (c *converter) block(n ast.Node, depth int) []notion.Block {
	switch node := n.(type) {
	case *ast.Heading:
		level := node.Level
		if level > 3 {
			level = 3 // Notion has only heading_1..3
		}
		return c.emit(notion.Block{Type: fmt.Sprintf("heading_%d", level), RichText: c.spans(node)})
	case *ast.Paragraph, *ast.TextBlock:
		return c.emit(notion.Block{Type: "paragraph", RichText: c.spans(n)})
	case *ast.List:
		return c.list(node, depth)
	case *ast.Blockquote:
		return c.quote(node, depth)
	case *ast.ThematicBreak:
		return []notion.Block{{Type: "divider"}}
	case *ast.FencedCodeBlock:
		return c.emit(notion.Block{Type: "code", Language: CanonicalLanguage(string(node.Language(c.src))), RichText: []notion.Span{{Content: c.codeText(node)}}})
	case *ast.CodeBlock:
		return c.emit(notion.Block{Type: "code", Language: "plain text", RichText: []notion.Span{{Content: c.codeText(node)}}})
	case *extast.Table:
		c.warn("table rendered as a plain code block (native tables not supported yet)")
		return c.emit(notion.Block{Type: "code", Language: "plain text", RichText: []notion.Span{{Content: c.rawSource(node)}}})
	case *ast.HTMLBlock:
		c.warn("raw HTML rendered as a plain code block")
		return c.emit(notion.Block{Type: "code", Language: "html", RichText: []notion.Span{{Content: c.htmlText(node)}}})
	default:
		// Unknown block: preserve its text as a paragraph rather than drop it.
		txt := c.rawSource(n)
		if strings.TrimSpace(txt) == "" {
			return nil
		}
		return c.emit(notion.Block{Type: "paragraph", RichText: []notion.Span{{Content: txt}}})
	}
}

// list converts a list node, mapping each item to a list-item block. A child
// sub-list becomes the item's Children up to maxNestDepth; deeper nesting is
// promoted to the current level with a single warning so no text is lost. The
// promoted blocks are appended AFTER the parent item, so reading order holds.
func (c *converter) list(node *ast.List, depth int) []notion.Block {
	itemType := "bulleted_list_item"
	if node.IsOrdered() {
		itemType = "numbered_list_item"
	}
	var out []notion.Block
	for item := node.FirstChild(); item != nil; item = item.NextSibling() {
		li, _ := item.(*ast.ListItem)
		if li == nil {
			continue
		}
		block := notion.Block{Type: itemType}
		if checked, ok := taskState(li); ok {
			block.Type = "to_do"
			block.Checked = checked
		}
		var promoted []notion.Block // deep content lifted to this level
		for sub := li.FirstChild(); sub != nil; sub = sub.NextSibling() {
			switch child := sub.(type) {
			case *ast.List:
				if depth >= maxNestDepth {
					c.warnNesting()
					promoted = append(promoted, c.list(child, depth)...)
				} else {
					block.Children = append(block.Children, c.list(child, depth+1)...)
				}
			default:
				switch {
				case len(block.RichText) == 0 && isTextual(child):
					block.RichText = c.spans(child) // first textual child = the item's own text
				case depth >= maxNestDepth:
					c.warnNesting()
					promoted = append(promoted, c.block(child, depth)...)
				default:
					block.Children = append(block.Children, c.block(child, depth+1)...)
				}
			}
		}
		out = append(out, c.emit(block)...)
		out = append(out, promoted...) // AFTER the parent → reading order preserved
	}
	return out
}

// quote converts a blockquote: its first textual child becomes the quote's own
// text, everything else becomes children (or is promoted past the depth cap).
// Iterating once avoids the double-count a separate text/children pass would hit
// when the first child is not a paragraph.
func (c *converter) quote(node *ast.Blockquote, depth int) []notion.Block {
	block := notion.Block{Type: "quote"}
	var promoted []notion.Block
	for sub := node.FirstChild(); sub != nil; sub = sub.NextSibling() {
		switch {
		case len(block.RichText) == 0 && isTextual(sub):
			block.RichText = c.spans(sub)
		case depth >= maxNestDepth:
			c.warnNesting()
			promoted = append(promoted, c.block(sub, depth)...)
		default:
			block.Children = append(block.Children, c.block(sub, depth+1)...)
		}
	}
	return append(c.emit(block), promoted...)
}

// spans extracts plain text from a node's inline children (Task 7 adds
// annotations). Splitting is emit's job, so this returns raw spans.
func (c *converter) spans(n ast.Node) []notion.Span {
	txt := c.inlineText(n)
	if txt == "" {
		return nil
	}
	return []notion.Span{{Content: txt}}
}

// inlineText concatenates the textual content of a node's inline descendants.
func (c *converter) inlineText(n ast.Node) string {
	var b strings.Builder
	ast.Walk(n, func(x ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := x.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(c.src))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte('\n')
			}
		case *ast.String:
			b.Write(t.Value)
		case *ast.AutoLink:
			b.Write(t.URL(c.src))
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

func (c *converter) codeText(n ast.Node) string {
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		b.Write(lines.At(i).Value(c.src))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (c *converter) htmlText(n *ast.HTMLBlock) string { return c.codeText(n) }

// rawSource returns the original source spanning a node's text descendants, a
// best-effort preservation of a construct we cannot map (e.g. a table).
func (c *converter) rawSource(n ast.Node) string {
	start, stop := -1, -1
	ast.Walk(n, func(x ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if t, ok := x.(*ast.Text); ok {
			s := t.Segment
			if start < 0 || s.Start < start {
				start = s.Start
			}
			if s.Stop > stop {
				stop = s.Stop
			}
		}
		return ast.WalkContinue, nil
	})
	if start < 0 {
		return ""
	}
	return string(c.src[start:stop])
}

// isTextual reports whether a node is a paragraph or the tight-list TextBlock
// goldmark emits for `- item` — both hold a list item's own inline text.
func isTextual(n ast.Node) bool {
	switch n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return true
	}
	return false
}

// firstTextual returns the first paragraph/TextBlock child, or nil.
func firstTextual(n ast.Node) ast.Node {
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if isTextual(ch) {
			return ch
		}
	}
	return nil
}

// taskState reports whether a list item is a GFM task item and its checked
// state. In goldmark the checkbox is the first inline child of the item's first
// textual child — which is a TextBlock in a tight list, a Paragraph in a loose
// one, so both must be accepted (a Paragraph-only check misses the common
// `- [ ]` tight case entirely).
func taskState(li *ast.ListItem) (checked bool, ok bool) {
	t := firstTextual(li)
	if t == nil {
		return false, false
	}
	if cb, isCB := t.FirstChild().(*extast.TaskCheckBox); isCB {
		return cb.IsChecked, true
	}
	return false, false
}
```

> Implementer note: goldmark's exact accessor names (`Segment.Value`, `List.IsOrdered`, `FencedCodeBlock.Language`, `Node.Lines`, `extast.TaskCheckBox.IsChecked`, `AutoLink.URL`) are for v1.7.x. If a name differs in the resolved version, let the RED tests drive the fix — the behavior the tests assert is the contract, not these accessor names. The list/quote **structure** (tight-list `TextBlock`, promote-after-parent order, single-pass quote) is not a naming detail: keep it as written.

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/markdown/ -race -v`
Expected: PASS (Task 5 tests still green, Task 6 tests green).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/markdown/markdown.go internal/markdown/markdown_test.go
git commit -m "feat(markdown): ToBlocks walker with GFM, nesting cap, and degradation"
```

---

## Task 7: inline formatting — bold / italic / code / strikethrough / link

Replaces the plain-text span extraction with styled spans. Delivers full inline formatting.

**Files:**
- Modify: `internal/markdown/markdown.go` (span extraction only)
- Test: `internal/markdown/inline_test.go`

**Interfaces:**
- Consumes: same as Task 6.
- Produces: no signature change — `ToBlocks` now emits annotated `notion.Span`s. Internally replaces `inlineText`/`spans` with a styled walker.

- [ ] **Step 1: Write the failing test** (`internal/markdown/inline_test.go`)

```go
package markdown

import "testing"

func firstParaSpans(t *testing.T, src string) []struct {
	Content                    string
	Bold, Italic, Code, Strike bool
	Link                       string
} {
	t.Helper()
	blocks, _, _ := ToBlocks([]byte(src))
	if len(blocks) == 0 {
		t.Fatal("no blocks")
	}
	var out []struct {
		Content                    string
		Bold, Italic, Code, Strike bool
		Link                       string
	}
	for _, s := range blocks[0].RichText {
		out = append(out, struct {
			Content                    string
			Bold, Italic, Code, Strike bool
			Link                       string
		}{s.Content, s.Bold, s.Italic, s.Code, s.Strikethrough, s.Link})
	}
	return out
}

func TestInlineBoldItalicCodeStrikeLink(t *testing.T) {
	spans := firstParaSpans(t, "plain **bold** *it* `code` ~~gone~~ [txt](https://x.test)\n")
	// Find each style at least once.
	var sawBold, sawItalic, sawCode, sawStrike, sawLink bool
	for _, s := range spans {
		sawBold = sawBold || (s.Bold && s.Content == "bold")
		sawItalic = sawItalic || (s.Italic && s.Content == "it")
		sawCode = sawCode || (s.Code && s.Content == "code")
		sawStrike = sawStrike || (s.Strike && s.Content == "gone")
		sawLink = sawLink || (s.Link == "https://x.test" && s.Content == "txt")
	}
	if !(sawBold && sawItalic && sawCode && sawStrike && sawLink) {
		t.Fatalf("missing an inline style: %+v", spans)
	}
}

func TestInlineNestedBoldItalicCombine(t *testing.T) {
	spans := firstParaSpans(t, "***both***\n")
	found := false
	for _, s := range spans {
		if s.Content == "both" && s.Bold && s.Italic {
			found = true
		}
	}
	if !found {
		t.Fatalf("nested emphasis must combine bold+italic: %+v", spans)
	}
}

func TestInlineLinkInsideBold(t *testing.T) {
	spans := firstParaSpans(t, "**see [here](https://x.test)**\n")
	for _, s := range spans {
		if s.Content == "here" && !(s.Bold && s.Link == "https://x.test") {
			t.Fatalf("link inside bold must carry both: %+v", s)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/markdown/ -run TestInline -v`
Expected: FAIL — spans are unstyled (plain), so `sawBold` etc. are false.

- [ ] **Step 3: Write the implementation** (edit `internal/markdown/markdown.go`)

Replace the body of `spans` with a styled recursion and **delete** the now-unused `inlineText` method (`rawSource`/`codeText` do their own source slicing and don't use it; `go vet` will flag it if left). Splitting stays in `emit` — `spans` returns raw styled spans.

```go
// spans extracts styled spans from a node's inline children. Splitting is
// emit's job, so this returns raw (unsplit) spans.
func (c *converter) spans(n ast.Node) []notion.Span {
	var raw []notion.Span
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		raw = append(raw, c.inlineSpans(ch, notion.Span{})...)
	}
	return raw
}

// inlineSpans converts one inline node into styled spans, carrying the ambient
// style (bold/italic/etc.) inherited from enclosing emphasis/link nodes down
// into the leaf text.
func (c *converter) inlineSpans(n ast.Node, style notion.Span) []notion.Span {
	switch node := n.(type) {
	case *ast.Text:
		s := style
		s.Content = string(node.Segment.Value(c.src))
		if node.SoftLineBreak() || node.HardLineBreak() {
			s.Content += "\n"
		}
		if s.Content == "" {
			return nil
		}
		return []notion.Span{s}
	case *ast.String:
		s := style
		s.Content = string(node.Value)
		if s.Content == "" {
			return nil
		}
		return []notion.Span{s}
	case *ast.CodeSpan:
		s := style
		s.Code = true
		s.Content = string(node.Text(c.src))
		return []notion.Span{s}
	case *ast.Emphasis:
		st := style
		if node.Level == 2 {
			st.Bold = true
		} else {
			st.Italic = true
		}
		return c.childSpans(node, st)
	case *extast.Strikethrough:
		st := style
		st.Strikethrough = true
		return c.childSpans(node, st)
	case *ast.Link:
		st := style
		st.Link = string(node.Destination)
		return c.childSpans(node, st)
	case *ast.AutoLink:
		s := style
		u := string(node.URL(c.src))
		s.Content, s.Link = u, u
		return []notion.Span{s}
	case *ast.Image:
		st := style
		st.Link = string(node.Destination)
		alt := string(node.Text(c.src))
		if alt == "" {
			alt = string(node.Destination)
		}
		st.Content = alt
		c.warn("image %q rendered as a link (native image block not supported yet)", string(node.Destination))
		return []notion.Span{st}
	default:
		// Unknown inline (e.g. raw HTML span): fall back to its text.
		s := style
		s.Content = string(node.Text(c.src))
		if s.Content == "" {
			return nil
		}
		return []notion.Span{s}
	}
}

// childSpans recurses into a node's children with an updated ambient style.
func (c *converter) childSpans(n ast.Node, style notion.Span) []notion.Span {
	var out []notion.Span
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		out = append(out, c.inlineSpans(ch, style)...)
	}
	return out
}
```

Delete the now-unused `inlineText` method (keep `codeText`, `rawSource`; `block()`/`list()`/`quote()` all reach text through `spans`, which is what changed). Run `go vet` to confirm nothing else referenced `inlineText`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/markdown/ -race -v`
Expected: PASS (Task 6 structural tests still green — the `indexMentioning`/degradation assertions hold since content is unchanged, only annotations are added — plus Task 7 inline tests green).

- [ ] **Step 5: Commit**

```bash
git add internal/markdown/markdown.go internal/markdown/inline_test.go
git commit -m "feat(markdown): inline formatting (bold, italic, code, strike, link)"
```

---

## Task 8: service `replaceBody` and body-aware write methods

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go` (update existing call sites + new tests)

**Interfaces:**
- Consumes: `notion.Block`, `ListBlockChildren`, `AppendBlockChildren`, `DeleteBlock`, `ErrAmbiguousWrite` (Tasks 1–4); existing `tracker.Fields`.
- Produces:
  - `type BodyRequest struct { Blocks []notion.Block; Progress io.Writer }`
  - `type BodyResult struct { BlocksWritten, BlocksDeleted int; Warnings []string }`
  - `type BodyWriteError struct { … }` (Error/Unwrap)
  - `Result` gains `Body *BodyResult`.
  - New signatures: `Upsert(ctx, f tracker.Fields, body *BodyRequest) (Result, error)`, `Set(ctx, f, body *BodyRequest) (Result, error)`, `SetByID(ctx, pageID string, f tracker.Fields, body *BodyRequest) (Result, error)`. `body == nil` ⇒ page body untouched (prior behavior).

- [ ] **Step 1: Write the failing test** (add to `internal/service/service_test.go`; extend `routes` to answer block endpoints)

```go
// bodyRoutes answers schema/query/create/update plus the three block endpoints,
// recording "METHOD path" order so a test can assert snapshot→append→delete.
func bodyRoutes(t *testing.T, queryResults, children string, seen *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + queryResults + `],"has_more":false}`))
		case r.URL.Path == "/v1/pages":
			w.Write([]byte(rowJSON))
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == http.MethodGet:
			w.Write([]byte(`{"results":[` + children + `],"has_more":false}`))
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == http.MethodPatch:
			w.Write([]byte(`{}`))
		default: // PATCH /v1/pages/{id}, DELETE /v1/blocks/{id}
			w.Write([]byte(rowJSON))
		}
	}))
}

func TestReplaceBodyOrdersSnapshotAppendDelete(t *testing.T) {
	var seen []string
	srv := bodyRoutes(t, rowJSON, `{"id":"old1","type":"paragraph"}`, &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL), notion.WithSleep(func(time.Duration) {})), testProfile())
	body := &BodyRequest{Blocks: []notion.Block{{Type: "paragraph", RichText: []notion.Span{{Content: "new"}}}}}
	res, err := s.Set(context.Background(), tracker.Fields{Ticket: "BDF-231"}, body)
	if err != nil {
		t.Fatalf("Set with body: %v", err)
	}
	// Assert relative order: children GET (snapshot) before children PATCH
	// (append) before /v1/blocks/old1 DELETE.
	iGet := indexOf(seen, "GET", "/children")
	iPatch := indexOf(seen, "PATCH", "/children")
	iDel := indexOf(seen, "DELETE", "/blocks/old1")
	if !(iGet >= 0 && iGet < iPatch && iPatch < iDel) {
		t.Fatalf("order wrong: %v (get=%d patch=%d del=%d)", seen, iGet, iPatch, iDel)
	}
	if res.Body == nil || res.Body.BlocksWritten != 1 || res.Body.BlocksDeleted != 1 {
		t.Fatalf("body result = %+v", res.Body)
	}
}

func TestReplaceBodySkipsChildPageOnDelete(t *testing.T) {
	var seen []string
	srv := bodyRoutes(t, rowJSON, `{"id":"sub1","type":"child_page"}`, &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL), notion.WithSleep(func(time.Duration) {})), testProfile())
	res, err := s.Set(context.Background(), tracker.Fields{Ticket: "BDF-231"},
		&BodyRequest{Blocks: []notion.Block{{Type: "paragraph", RichText: []notion.Span{{Content: "x"}}}}})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if indexOf(seen, "DELETE", "/blocks/sub1") >= 0 {
		t.Fatal("child_page must NOT be deleted")
	}
	if len(res.Body.Warnings) == 0 {
		t.Fatal("skipping a child_page must produce a warning")
	}
}

func TestUpsertWithNilBodyLeavesBodyUntouched(t *testing.T) {
	var seen []string
	srv := bodyRoutes(t, "", "", &seen)
	defer srv.Close()
	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	if _, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231"}, nil); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	for _, e := range seen {
		if strings.Contains(e, "/children") || strings.Contains(e, "/blocks/") {
			t.Fatalf("nil body must touch no block endpoint, saw %q", e)
		}
	}
}

func TestSetBodyAppendFailureKeepsPropertiesApplied(t *testing.T) {
	// The children PATCH (append) 400s. Properties were already written, so
	// the caller must get a *BodyWriteError with the page still populated.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + rowJSON + `],"has_more":false}`))
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == http.MethodGet:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"code":"validation_error","message":"bad block"}`))
		default: // PATCH /v1/pages/{id}
			w.Write([]byte(rowJSON))
		}
	}))
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL), notion.WithSleep(func(time.Duration) {})), testProfile())
	res, err := s.Set(context.Background(), tracker.Fields{Ticket: "BDF-231"},
		&BodyRequest{Blocks: []notion.Block{{Type: "paragraph", RichText: []notion.Span{{Content: "x"}}}}})
	var bwe *BodyWriteError
	if !errors.As(err, &bwe) {
		t.Fatalf("append failure must be a *BodyWriteError, got %v", err)
	}
	if res.Page.ID == "" {
		t.Fatal("properties were written, so Result.Page must be populated")
	}
	if res.Action != "updated" {
		t.Fatalf("action = %q, want updated", res.Action)
	}
}

// indexOf returns the position of the first "METHOD …suffix" entry, or -1.
func indexOf(seen []string, method, suffix string) int {
	for i, e := range seen {
		if strings.HasPrefix(e, method+" ") && strings.Contains(e, suffix) {
			return i
		}
	}
	return -1
}
```

Add `"io"`, `"strings"`, `"time"` to the service_test imports if missing (`errors` is already imported).

**Call-site updates required in THIS task (or the tree does not compile / `go test ./...` is red at the task's end):**
- `internal/service/service_test.go` — every existing `s.Upsert(...)`, `s.Set(...)` call: add a trailing `nil`.
- `internal/service/pageid_test.go` — every `s.SetByID(ctx, id, fields)` (there are ~5, around lines 91/137/175/222/278): add a trailing `nil`.
- `internal/cli/upsert.go` and `internal/cli/set.go` — the production call sites: pass `nil` as the new `body` argument for now (Task 9 replaces `nil` with the real `--body-file` wiring). This keeps `go test ./... -race` green at the end of Task 8, honoring the plan's global constraint.

Run `grep -rn 'SetByID\|\.Upsert(\|\.Set(' internal/` first to catch any call site this list missed.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/service/ -run 'TestReplaceBody|TestUpsertWithNil' -v`
Expected: FAIL — compile errors (signatures take a new arg), `undefined: BodyRequest`.

- [ ] **Step 3: Write the implementation**

Add `"io"` to `internal/service/service.go` imports. Add the types and `replaceBody`, and thread `body` through the three methods.

```go
// BodyRequest carries an optional Markdown body to replace on a page. A nil
// *BodyRequest means "leave the page body untouched" (the pre-body behavior).
type BodyRequest struct {
	Blocks   []notion.Block
	Progress io.Writer // optional; ephemeral progress lines go here (stderr)
}

// BodyResult reports what replaceBody did, for --json and stderr warnings.
type BodyResult struct {
	BlocksWritten int
	BlocksDeleted int
	Warnings      []string // e.g. skipped child_page/child_database
}

// BodyWriteError marks a failure that happened AFTER the row's properties were
// written (or the row created): properties are applied, only the body replace
// failed. It unwraps to the underlying error so exitCodeFor and
// errors.Is(…, notion.ErrAmbiguousWrite) keep working.
type BodyWriteError struct{ err error }

func (e *BodyWriteError) Error() string { return e.err.Error() }
func (e *BodyWriteError) Unwrap() error { return e.err }

// replaceBody makes the page body equal to req.Blocks with replace semantics:
// snapshot existing children, append the new body at the end, then delete the
// snapshotted children — skipping child_page/child_database so sub-pages are
// never archived. The order converges on re-run if append fails midway. On a
// failed DELETE, res already carries the real BlocksWritten (the append DID
// happen), which the CLI surfaces even in the partial-failure JSON (spec §8).
func (s *Service) replaceBody(ctx context.Context, pageID string, req *BodyRequest) (BodyResult, error) {
	var res BodyResult
	old, err := s.client.ListBlockChildren(ctx, pageID)
	if err != nil {
		return res, err
	}
	// Count how many of the snapshot we will actually delete (sub-pages are kept).
	toDelete := 0
	for _, ch := range old {
		if ch.Type != "child_page" && ch.Type != "child_database" {
			toDelete++
		}
	}
	progress(req.Progress, "appending %d block(s)…", len(req.Blocks))
	if err := s.client.AppendBlockChildren(ctx, pageID, req.Blocks); err != nil {
		return res, err
	}
	res.BlocksWritten = len(req.Blocks)
	for _, ch := range old {
		if ch.Type == "child_page" || ch.Type == "child_database" {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("kept a %s (%s): deleting it would archive a sub-page or database", ch.Type, ch.ID))
			continue
		}
		if err := s.client.DeleteBlock(ctx, ch.ID); err != nil {
			return res, err // res.BlocksWritten is already set: the append succeeded
		}
		res.BlocksDeleted++
		progress(req.Progress, "deleted %d/%d old block(s)", res.BlocksDeleted, toDelete)
	}
	return res, nil
}

func progress(w io.Writer, format string, args ...any) {
	if w != nil {
		fmt.Fprintf(w, format+"\n", args...)
	}
}

// withBody appends the body (if any) after the properties/row already exist,
// wrapping a body failure so the caller knows properties were applied.
func (s *Service) withBody(ctx context.Context, page notion.Page, action string, body *BodyRequest) (Result, error) {
	res := Result{Action: action, Page: page}
	if body == nil {
		return res, nil
	}
	br, err := s.replaceBody(ctx, page.ID, body)
	res.Body = &br
	if err != nil {
		return res, &BodyWriteError{err: err}
	}
	return res, nil
}
```

Now rewrite the three methods to end in `withBody`. `Upsert`:

```go
func (s *Service) Upsert(ctx context.Context, f tracker.Fields, body *BodyRequest) (Result, error) {
	if f.Ticket == "" {
		return Result{}, ErrEmptyTicket
	}
	matches, err := s.findByTicket(ctx, f.Ticket)
	if err != nil {
		return Result{}, err
	}
	decision, err := tracker.Decide(f.Ticket, matches)
	if err != nil {
		return Result{}, err
	}
	schema, err := s.Schema(ctx)
	if err != nil {
		return Result{}, err
	}
	props, err := tracker.BuildProperties(f, s.profile.Properties, schema)
	if err != nil {
		return Result{}, err
	}
	var page notion.Page
	action := "updated"
	if decision.Action == tracker.ActionCreate {
		page, err = s.client.CreatePage(ctx, s.profile.DataSourceID, props)
		action = "created"
	} else {
		page, err = s.client.UpdatePage(ctx, decision.PageID, props)
	}
	if err != nil {
		return Result{Action: action}, err
	}
	return s.withBody(ctx, page, action, body)
}
```

`Set`:

```go
func (s *Service) Set(ctx context.Context, f tracker.Fields, body *BodyRequest) (Result, error) {
	if f.Ticket == "" {
		return Result{}, ErrEmptyTicket
	}
	matches, err := s.findByTicket(ctx, f.Ticket)
	if err != nil {
		return Result{}, err
	}
	if len(matches) == 0 {
		return Result{}, fmt.Errorf("%w: %s", ErrNotFound, f.Ticket)
	}
	decision, err := tracker.Decide(f.Ticket, matches)
	if err != nil {
		return Result{}, err
	}
	schema, err := s.Schema(ctx)
	if err != nil {
		return Result{}, err
	}
	props, err := tracker.BuildProperties(f, s.profile.Properties, schema)
	if err != nil {
		return Result{}, err
	}
	page, err := s.client.UpdatePage(ctx, decision.PageID, props)
	if err != nil {
		return Result{Action: "updated"}, err
	}
	return s.withBody(ctx, page, "updated", body)
}
```

`SetByID`:

```go
func (s *Service) SetByID(ctx context.Context, pageID string, f tracker.Fields, body *BodyRequest) (Result, error) {
	page, err := s.resolvePage(ctx, pageID, true)
	if err != nil {
		return Result{}, err
	}
	schema, err := s.Schema(ctx)
	if err != nil {
		return Result{}, err
	}
	props, err := tracker.BuildProperties(f, s.profile.Properties, schema)
	if err != nil {
		return Result{}, err
	}
	updated, err := s.client.UpdatePage(ctx, page.ID, props)
	if err != nil {
		return Result{Action: "updated"}, err
	}
	return s.withBody(ctx, updated, "updated", body)
}
```

Add `Body *BodyResult` to `Result`:

```go
type Result struct {
	Action string
	Page   notion.Page
	Body   *BodyResult // non-nil only when a body was written
}
```

- [ ] **Step 4: Run to verify it passes (whole tree, per the global constraint)**

Run: `go test ./... -race`
Expected: PASS. This must include `internal/service` (new + updated tests), `internal/cli` (compiles because upsert.go/set.go now pass `nil`), and `internal/service/pageid_test.go` (call sites updated). If `internal/cli` fails to compile, a `SetByID`/`Set`/`Upsert` call site was missed — fix it before committing.

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go internal/service/pageid_test.go internal/cli/upsert.go internal/cli/set.go
git commit -m "feat(service): replaceBody and body-aware Upsert/Set/SetByID"
```

---

## Task 9: CLI `--body-file` on `upsert` and `set`

**Files:**
- Create: `internal/cli/body.go`, `internal/cli/body_test.go`
- Modify: `internal/cli/upsert.go`, `internal/cli/set.go`, `internal/cli/output.go`

**Interfaces:**
- Consumes: `markdown.ToBlocks`, `notion.ValidateAppendable`, `service.BodyRequest/BodyResult/BodyWriteError`, new service signatures (Task 8).
- Produces: `--body-file` flag on `upsert` and `set`; `loadBody`, `emitWrite`, `printWarnings` helpers; `exitCodeFor` returns `ExitError` for `*service.BodyWriteError`.

- [ ] **Step 1: Write the failing tests** (`internal/cli/body_test.go`)

Follow the existing cli test style (drive through `executeArgs` with the seams `loadConfig`/`newClient` pointed at an httptest server). Reuse whatever fixture helper the existing `upsert_test.go` uses; if it exposes a `withServer(t, handler)` helper, use it — otherwise replicate its setup.

```go
package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/spf13/cobra"
)

func TestLoadBodyRejectsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.md")
	os.WriteFile(p, []byte("   \n"), 0o600)
	_, _, err := loadBody(p, nil, nil)
	if err == nil || exitCodeFor(err) != ExitUsage {
		t.Fatalf("empty file must be a usage error, got %v (code %d)", err, exitCodeFor(err))
	}
}

func TestLoadBodyRejectsMissingFile(t *testing.T) {
	_, _, err := loadBody(filepath.Join(t.TempDir(), "nope.md"), nil, nil)
	if err == nil || exitCodeFor(err) != ExitUsage {
		t.Fatalf("missing file must be a usage error, got %v", err)
	}
}

func TestLoadBodyReadsStdin(t *testing.T) {
	req, _, err := loadBody("-", strings.NewReader("# Title\n\nbody\n"), nil)
	if err != nil {
		t.Fatalf("stdin body: %v", err)
	}
	if len(req.Blocks) == 0 {
		t.Fatal("stdin produced no blocks")
	}
}

// Partial failure e2e: properties are written, then the body append 400s. The
// command must exit 1 (NOT 2, despite the underlying 400) and, with --json,
// still print a parsable object marking the body as unwritten. This is the
// most delicate public contract of the feature (spec §8).
func TestUpsertBodyAppendFailureExitsOneWithParsableJSON(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == http.MethodGet:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"code":"validation_error","message":"bad block"}`))
		default: // PATCH /v1/pages/{id}
			w.Write([]byte(cliRowJSON))
		}
	})
	md := filepath.Join(t.TempDir(), "body.md")
	os.WriteFile(md, []byte("# Title\n\nbody\n"), 0o600)

	var code int
	out := captureStdout(t, func() {
		code = executeArgs([]string{"upsert", "--ticket", "BDF-231", "--body-file", md, "--json", "--config", cfg})
	})
	if code != ExitError {
		t.Fatalf("partial failure must exit %d, got %d", ExitError, code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout must stay parsable JSON on partial failure: %v\n%s", err, out)
	}
	body, ok := got["body"].(map[string]any)
	if !ok || body["written"] != false || body["error"] == nil {
		t.Fatalf("body must mark written:false with an error: %v", got)
	}
	if got["page"] == nil {
		t.Fatal("page (applied properties) must still be present")
	}
}

// emitWrite must route warnings to stderr, never stdout (which carries --json).
func TestEmitWriteSendsWarningsToStderrNotStdout(t *testing.T) {
	cmd := &cobra.Command{}
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)

	res := service.Result{Action: "updated", Body: &service.BodyResult{Warnings: []string{"kept a child_page"}}}
	_ = emitWrite(cmd, cliProps(), res, []string{"table degraded"}, false, nil)

	if !strings.Contains(errBuf.String(), "table degraded") || !strings.Contains(errBuf.String(), "child_page") {
		t.Fatalf("warnings must be on stderr: %q", errBuf.String())
	}
	if strings.Contains(out.String(), "warning") {
		t.Fatalf("warnings must NOT be on stdout: %q", out.String())
	}
}
```

> `cliProps()` returns the same `config.Properties` the stub config maps (ticket=Ticket, status=Stato, title=Name). Add it as a one-line test helper if one does not already exist:
> ```go
> func cliProps() config.Properties {
> 	return config.Properties{Ticket: "Ticket", Status: "Stato", Title: "Name"}
> }
> ```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cli/ -run 'TestLoadBody|TestUpsertBodyAppend|TestEmitWrite' -v`
Expected: FAIL — `undefined: loadBody` / `emitWrite`.

- [ ] **Step 3: Write the implementation**

`internal/cli/body.go`:

```go
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/markdown"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/spf13/cobra"
)

// maxBodyFileBytes is the pre-flight cap on a --body-file (spec §9): a task
// body over 1 MiB of Markdown is out of scope, and rejecting it up front beats
// dying mid-replace.
const maxBodyFileBytes = 1 << 20

// loadBody reads and parses a --body-file into a validated BodyRequest, all
// before any network call. path "-" reads stdin. Every input problem is a
// usage error (exit 2). progress is where the service later writes ephemeral
// progress lines (stderr).
func loadBody(path string, stdin io.Reader, progress io.Writer) (*service.BodyRequest, []string, error) {
	raw, err := readBodySource(path, stdin)
	if err != nil {
		return nil, nil, Errorf(ExitUsage, "reading body file %s: %v", path, err)
	}
	if len(raw) > maxBodyFileBytes {
		return nil, nil, Errorf(ExitUsage, "body file %s is over the %d-byte limit", path, maxBodyFileBytes)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil, nil, Errorf(ExitUsage, "body file %s is empty", path)
	}
	blocks, warnings, err := markdown.ToBlocks(raw)
	if err != nil {
		return nil, nil, Errorf(ExitUsage, "parsing %s: %v", path, err)
	}
	if err := notion.ValidateAppendable(blocks); err != nil {
		return nil, nil, Errorf(ExitUsage, "%v", err)
	}
	return &service.BodyRequest{Blocks: blocks, Progress: progress}, warnings, nil
}

func readBodySource(path string, stdin io.Reader) ([]byte, error) {
	// Read one byte past the cap so the size check can detect an over-limit file.
	limit := int64(maxBodyFileBytes) + 1
	if path == "-" {
		if stdin == nil {
			return nil, fmt.Errorf("no stdin")
		}
		return io.ReadAll(io.LimitReader(stdin, limit))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// printWarnings writes each warning to w (stderr) with a "warning: " prefix.
func printWarnings(w io.Writer, warnings []string) {
	for _, msg := range warnings {
		fmt.Fprintln(w, "warning: "+msg)
	}
}

// emitWrite is the shared output path for upsert/set: it prints warnings to
// stderr, then either the success --json (with the additive body object) or,
// on a body failure after properties were written, a parsable partial-failure
// --json, and finally returns err so the process exits with the right code.
func emitWrite(cmd *cobra.Command, props config.Properties, res service.Result, warnings []string, asJSON bool, err error) error {
	printWarnings(cmd.ErrOrStderr(), warnings)
	if res.Body != nil {
		printWarnings(cmd.ErrOrStderr(), res.Body.Warnings)
	}
	if err != nil {
		var bwe *service.BodyWriteError
		if errors.As(err, &bwe) && asJSON {
			body := map[string]any{"written": false, "error": bwe.Error()}
			if res.Body != nil {
				// Real counts of what happened before the failure: crucial in the
				// dual case (append ok, a DELETE failed) where the body WAS written
				// (spec §8).
				body["blocks_written"] = res.Body.BlocksWritten
				body["blocks_deleted"] = res.Body.BlocksDeleted
			}
			_ = printJSON(cmd.OutOrStdout(), map[string]any{
				"action": res.Action,
				"page":   toPageJSON(res.Page, props),
				"body":   body,
			})
		}
		return err
	}
	if asJSON {
		out := map[string]any{"action": res.Action, "page": toPageJSON(res.Page, props)}
		if res.Body != nil {
			out["body"] = map[string]any{
				"blocks_written": res.Body.BlocksWritten,
				"blocks_deleted": res.Body.BlocksDeleted,
			}
		}
		return printJSON(cmd.OutOrStdout(), out)
	}
	return nil
}
```

Add the flag to the shared binder in `internal/cli/upsert.go` (`writeFlags`), so both commands get it:

```go
// in writeFlags struct:
	bodyFile string
// in bindShared:
	cmd.Flags().StringVar(&wf.bodyFile, "body-file", "",
		"Markdown file whose content replaces the page body ('-' for stdin); replace semantics, owns the body")
```

Rewrite `newUpsertCmd`'s RunE tail:

```go
RunE: func(cmd *cobra.Command, _ []string) error {
	svc, err := buildService(cmd)
	if err != nil {
		return err
	}
	var body *service.BodyRequest
	var warnings []string
	if wf.bodyFile != "" {
		body, warnings, err = loadBody(wf.bodyFile, cmd.InOrStdin(), cmd.ErrOrStderr())
		if err != nil {
			return err
		}
	}
	res, err := svc.Upsert(cmd.Context(), wf.fields(), body)
	return emitWrite(cmd, svc.Profile().Properties, res, warnings, wf.asJSON, err)
},
```

Rewrite `newSetCmd`'s RunE tail:

```go
RunE: func(cmd *cobra.Command, _ []string) error {
	svc, err := buildService(cmd)
	if err != nil {
		return err
	}
	var body *service.BodyRequest
	var warnings []string
	if wf.bodyFile != "" {
		body, warnings, err = loadBody(wf.bodyFile, cmd.InOrStdin(), cmd.ErrOrStderr())
		if err != nil {
			return err
		}
	}
	var res service.Result
	if cmd.Flags().Changed("page-id") {
		res, err = svc.SetByID(cmd.Context(), wf.pageID, wf.fields(), body)
	} else {
		res, err = svc.Set(cmd.Context(), wf.fields(), body)
	}
	return emitWrite(cmd, svc.Profile().Properties, res, warnings, wf.asJSON, err)
},
```

Add the `BodyWriteError` case at the TOP of `exitCodeFor` in `internal/cli/output.go` (before the `apiErr.Status == 400` case, since a body error can wrap a 400 but must still exit 1):

```go
	// A body write that failed after properties were applied is a partial
	// success: exit 1 regardless of the underlying status, so a wrapped 400
	// does not masquerade as a usage error.
	var bodyErr *service.BodyWriteError
	if errors.As(err, &bodyErr) {
		return ExitError
	}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/cli/ -race -v`
Expected: PASS (existing upsert/set tests unaffected — flag is optional and the seams unchanged; new body tests green).

- [ ] **Step 5: Full suite + vet**

Run: `go test ./... -race` then `go vet ./...`
Expected: all PASS, vet clean.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/body.go internal/cli/body_test.go internal/cli/upsert.go internal/cli/set.go internal/cli/output.go
git commit -m "feat(cli): --body-file on upsert and set with additive --json and partial-failure exit"
```

---

## Task 10: documentation — README (en/it) and the agent skill

**Files:**
- Modify: `README.md`, `README.it.md`
- Modify: `skills/notion-track/SKILL.md`

No tests; the deliverable is accurate docs. Manual verification: the commands shown must match the flags implemented in Task 9.

- [ ] **Step 1: README Usage subsection (both languages)**

Add a `--body-file` entry under the `upsert`/`set` usage in `README.md` and its translation in `README.it.md`, covering, in prose:
- `notion-track upsert --ticket "X" --body-file notes.md` and `set --page-id <id> --body-file notes.md`; `-` reads stdin.
- **Replace semantics:** `--body-file` **owns the page body** — it replaces everything there, including content added by hand in Notion. Running it twice yields the same body.
- Supported Markdown subset (headings h1–h3, paragraphs, bulleted/numbered lists, task checkboxes, code fences, quotes, dividers, inline bold/italic/code/strikethrough/links) and that tables, images, raw HTML and nesting beyond 2 levels **degrade with a warning** rather than being dropped.
- Cost is **O(n)** in existing blocks (no bulk delete); large pages take time; progress prints to stderr.
- Concurrency: two runs against the same page can duplicate the body.

- [ ] **Step 2: Fix the skill's scope claim** (`skills/notion-track/SKILL.md`)

In "When NOT to reach for this skill", the current bullet reads:

> - Anything beyond task rows — page bodies, comments, arbitrary Notion pages, other databases — is out of scope; `notion-track` only touches this one board.

Replace it so page **bodies** are now in scope while the rest stays out:

> - Comments, arbitrary Notion pages, and other databases remain out of scope; `notion-track` only touches this one board. The page **body** is now writable via `upsert`/`set --body-file <file>` (Markdown, replace semantics — it **owns** the body and overwrites anything there, so read before you write). Sub-pages are preserved, not archived.

- [ ] **Step 3: Add a body example to the skill's command reference**

Under the `upsert`/`set` sections of `SKILL.md`, add the `--body-file` flag with the replace-semantics warning and note the additive `--json`: on success `body:{blocks_written,blocks_deleted}`; on partial failure (`exit 1`) `body:{written:false,error}` while `page` still reflects the applied properties.

- [ ] **Step 4: Verify docs match the build**

Run: `go build ./... && ./… --help` sanity is optional; instead grep that every flag/name in the docs exists:

```bash
grep -n "body-file" internal/cli/*.go
```
Expected: the flag is defined in `upsert.go` (shared binder) and reachable from both commands.

- [ ] **Step 5: Commit**

```bash
git add README.md README.it.md skills/notion-track/SKILL.md
git commit -m "docs: --body-file usage, and page bodies now in scope for the agent skill"
```

---

## Self-Review (completed by plan author)

**1. Spec coverage** — every spec section maps to a task:
- §2 subset/mapping/degradation/languages → Tasks 5 (language), 6 (structure+degradation), 7 (inline).
- §3 `Block`/`Span`/`ToBlocks` → Tasks 1, 6, 7.
- §4 retry mode + 3 endpoints + `ValidateAppendable` → Tasks 2, 3, 4.
- §5 chunking split of responsibility → Tasks 2 (client grouping), 5 (content split).
- §6 replace order + skip sub-pages + create path → Task 8.
- §7 CLI pre-flight + stdin + exit codes → Task 9.
- §8 `--json` additive + partial failure exit 1 → Tasks 8 (BodyWriteError) + 9 (emitWrite, exitCodeFor).
- §9 cost/progress/1 MiB cap → Tasks 8 (progress) + 9 (maxBodyFileBytes).
- §10 testing → tests in every task.
- §11 docs → Task 10.

**2. Placeholder scan** — no "TBD/handle edge cases"; the one goldmark accessor caveat is an explicit "let RED tests drive the exact name", not a missing spec. The 1 MiB cap, 450 KiB budget, and language set are concrete.

**3. Type consistency** — signatures used across tasks match: `ToBlocks(src []byte) ([]notion.Block, []string, error)`; `AppendBlockChildren(ctx, string, []notion.Block) error`; `Upsert/Set(ctx, tracker.Fields, *BodyRequest)`, `SetByID(ctx, string, tracker.Fields, *BodyRequest)`; `Result.Body *BodyResult`; `BodyWriteError` (Unwrap). `CanonicalLanguage`, `splitLongSpans`, `splitBlockOnSpanLimit`, `ValidateAppendable`, `splitIntoRequests`, `doRejectRetryable`, `rejectedByServer`, `ChildBlock{ID,Type}` are referenced with identical names throughout.

**4. Fable review round applied.** A second review (spec + plan together) found and this plan now fixes: the 2000-char split not reaching code/table/HTML/unknown blocks (now every text block goes through `emit`); the deep-nesting promotion emitting promoted content *before* its parent (now after, with an order test); the forgotten `SetByID` call sites in `pageid_test.go` and the two CLI call sites (Task 8 updates all of them and passes `nil` so the tree compiles); tight-list `to_do` recognition (`isTextual` accepts `TextBlock`); the depth-3 leak (guarded in the walker AND rejected pre-flight by `ValidateAppendable`'s new depth check); the blockquote first-paragraph duplication (single-pass `quote`); the broken `exitCodeFor` unit test (replaced by an end-to-end partial-failure test); the failure-JSON now carrying real `blocks_written`/`blocks_deleted`; and the `%w: %w` error chain. Spec decisions resolved in the same round: setext → h1/h2 (CommonMark), warnings name the construct (not the line), append progress is a single pre-append line (no per-chunk hook in the client).
