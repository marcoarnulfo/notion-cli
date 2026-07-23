package notion

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
