package notion

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

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

// TestDoRejectRetryableExhaustsRetriesReturnsUnderlyingAPIError pins that a
// persistent 429 (rejected every time, never applied) surfaces as the plain
// *APIError once retries are exhausted, NOT wrapped in ErrAmbiguousWrite: the
// server rejected the write on every attempt, so nothing was ever applied and
// there is nothing ambiguous about it.
func TestDoRejectRetryableExhaustsRetriesReturnsUnderlyingAPIError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"code":"rate_limited","message":"slow down"}`))
	}))
	defer srv.Close()

	c := New("tok", WithBaseURL(srv.URL), WithSleep(func(time.Duration) {}), WithMaxRetries(2))
	err := c.doRejectRetryable(context.Background(), http.MethodPatch, "/x", map[string]any{}, nil)
	if err == nil {
		t.Fatal("want error after exhausting retries, got nil")
	}
	if errors.Is(err, ErrAmbiguousWrite) {
		t.Fatalf("persistent rejection must NOT be ambiguous (nothing was applied), got %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 429 {
		t.Fatalf("want APIError 429, got %v", err)
	}
	if calls != 3 { // initial attempt + 2 retries
		t.Fatalf("want 3 calls (1 + 2 retries), got %d", calls)
	}
}

// TestSplitIntoRequestsChunksByByteBudget forces splitIntoRequests to split on
// the 450KiB byte budget rather than the 100-count or 1000-block limits: each
// block below carries a ~300KB rich-text span, well under the count/block caps
// but large enough that two of them exceed maxBytesPerRequest together.
func TestSplitIntoRequestsChunksByByteBudget(t *testing.T) {
	big := strings.Repeat("x", 300<<10) // 300KiB of ASCII content
	blocks := make([]Block, 4)
	for i := range blocks {
		blocks[i] = Block{Type: "paragraph", RichText: []Span{{Content: big}}}
	}
	batches := splitIntoRequests(blocks)
	if len(batches) < 2 {
		t.Fatalf("4x300KiB blocks must span >1 batch via the byte budget, got %d batch(es)", len(batches))
	}
	// Order preserved and no block dropped: reconstruct the flattened content
	// order and compare against the input order.
	var got []string
	for _, batch := range batches {
		for _, b := range batch {
			got = append(got, b.RichText[0].Content)
		}
	}
	if len(got) != len(blocks) {
		t.Fatalf("lost blocks: got %d, want %d", len(got), len(blocks))
	}
	for i := range blocks {
		if got[i] != blocks[i].RichText[0].Content {
			t.Fatalf("order not preserved at index %d", i)
		}
	}
	// Each batch must itself respect the byte budget.
	for bi, batch := range batches {
		sum := 0
		for _, b := range batch {
			sum += blockBytes(b)
		}
		if sum > maxBytesPerRequest {
			t.Fatalf("batch %d is %d bytes, over the %d budget", bi, sum, maxBytesPerRequest)
		}
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
