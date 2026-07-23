package markdown

import (
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
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
	types, _ := blockTypes("\ufeff# Title\r\n\r\nbody\r\n")
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
