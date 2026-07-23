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
