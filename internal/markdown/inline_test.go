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
