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
