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
// "\ufeff" is the BOM as an escape so it survives copy/paste of this file.
func normalize(src []byte) []byte {
	s := strings.ReplaceAll(strings.ReplaceAll(string(src), "\r\n", "\n"), "\r", "\n")
	return []byte(strings.TrimPrefix(s, "\ufeff"))
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
// annotations). Splitting is emit's job, so this returns raw spans. An image
// degrades to a link span (its alt text as content, its URL as the link) so
// it survives instead of being silently dropped.
func (c *converter) spans(n ast.Node) []notion.Span {
	var out []notion.Span
	ast.Walk(n, func(x ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := x.(type) {
		case *ast.Text:
			s := string(t.Segment.Value(c.src))
			if t.SoftLineBreak() || t.HardLineBreak() {
				s += "\n"
			}
			out = appendSpanText(out, s)
		case *ast.String:
			out = appendSpanText(out, string(t.Value))
		case *ast.AutoLink:
			out = appendSpanText(out, string(t.URL(c.src)))
		case *ast.Image:
			c.warn("image %q rendered as a link (images are not embedded yet)", string(t.Destination))
			out = append(out, notion.Span{Content: c.imageAlt(t), Link: string(t.Destination)})
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return out
}

// appendSpanText appends s to the last span's content, or starts a new plain
// span, so adjacent text/autolink runs collapse into one span.
func appendSpanText(spans []notion.Span, s string) []notion.Span {
	if s == "" {
		return spans
	}
	if n := len(spans); n > 0 && spans[n-1].Link == "" {
		spans[n-1].Content += s
		return spans
	}
	return append(spans, notion.Span{Content: s})
}

// imageAlt returns an image's alt text (its inline children's plain text).
func (c *converter) imageAlt(img *ast.Image) string {
	var b strings.Builder
	for ch := img.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if t, ok := ch.(*ast.Text); ok {
			b.Write(t.Segment.Value(c.src))
		}
	}
	return b.String()
}

func (c *converter) codeText(n ast.Node) string {
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(c.src))
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
