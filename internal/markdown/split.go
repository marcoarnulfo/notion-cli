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
