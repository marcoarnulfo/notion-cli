package notion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
