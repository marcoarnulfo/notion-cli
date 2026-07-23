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
