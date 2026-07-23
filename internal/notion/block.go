package notion

import "encoding/json"

// Block is one Notion block in the shape internal/markdown builds and the
// client serializes. MarshalJSON emits the nested {"type":X,"X":{…}} form the
// append endpoint expects. It is a pure data type: it performs no I/O and does
// not depend on the HTTP client, so internal/markdown can import it the same
// way internal/tracker already imports Schema.
type Block struct {
	Type     string // paragraph, heading_1..3, bulleted_list_item,
	// numbered_list_item, to_do, code, quote, divider
	RichText []Span
	Checked  bool    // to_do only
	Language string  // code only
	Children []Block // nested list/quote children; ≤2 levels materialized
}

// Span is a writable rich-text fragment with its annotations. It is kept
// separate from the read-oriented RichText/Text types in types.go so the read
// path stays untouched.
type Span struct {
	Content       string
	Link          string // url; "" when none
	Bold          bool
	Italic        bool
	Code          bool
	Strikethrough bool
}

func (s Span) toJSON() map[string]any {
	text := map[string]any{"content": s.Content}
	if s.Link != "" {
		text["link"] = map[string]string{"url": s.Link}
	}
	out := map[string]any{"type": "text", "text": text}
	ann := map[string]bool{}
	if s.Bold {
		ann["bold"] = true
	}
	if s.Italic {
		ann["italic"] = true
	}
	if s.Code {
		ann["code"] = true
	}
	if s.Strikethrough {
		ann["strikethrough"] = true
	}
	// Only attach annotations when at least one is set; Notion fills defaults.
	if len(ann) > 0 {
		out["annotations"] = ann
	}
	return out
}

// MarshalJSON emits the Notion append shape for a block.
func (b Block) MarshalJSON() ([]byte, error) {
	inner := map[string]any{}
	if b.Type != "divider" {
		spans := make([]map[string]any, 0, len(b.RichText))
		for _, s := range b.RichText {
			spans = append(spans, s.toJSON())
		}
		inner["rich_text"] = spans
	}
	if b.Type == "to_do" {
		inner["checked"] = b.Checked
	}
	if b.Type == "code" {
		inner["language"] = b.Language
	}
	if len(b.Children) > 0 {
		inner["children"] = b.Children
	}
	return json.Marshal(map[string]any{"type": b.Type, b.Type: inner})
}
