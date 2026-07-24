package notion

import (
	"encoding/json"
	"testing"
)

func marshalToMap(t *testing.T, b Block) map[string]any {
	t.Helper()
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func TestMarshalParagraphWithAnnotatedSpans(t *testing.T) {
	b := Block{Type: "paragraph", RichText: []Span{
		{Content: "plain "},
		{Content: "bold", Bold: true},
		{Content: "link", Link: "https://x.test"},
	}}
	m := marshalToMap(t, b)
	if m["type"] != "paragraph" {
		t.Fatalf("type = %v", m["type"])
	}
	para := m["paragraph"].(map[string]any)
	rt := para["rich_text"].([]any)
	if len(rt) != 3 {
		t.Fatalf("want 3 spans, got %d", len(rt))
	}
	first := rt[0].(map[string]any)
	if first["type"] != "text" {
		t.Fatalf("span type = %v", first["type"])
	}
	if first["text"].(map[string]any)["content"] != "plain " {
		t.Fatalf("content = %v", first["text"])
	}
	if _, hasAnn := first["annotations"]; hasAnn {
		t.Fatal("plain span must not carry annotations")
	}
	second := rt[1].(map[string]any)
	if second["annotations"].(map[string]any)["bold"] != true {
		t.Fatalf("bold span missing annotation: %v", second)
	}
	third := rt[2].(map[string]any)
	if third["text"].(map[string]any)["link"].(map[string]any)["url"] != "https://x.test" {
		t.Fatalf("link span missing url: %v", third)
	}
}

func TestMarshalDividerHasEmptyBody(t *testing.T) {
	m := marshalToMap(t, Block{Type: "divider"})
	body := m["divider"].(map[string]any)
	if len(body) != 0 {
		t.Fatalf("divider body should be empty, got %v", body)
	}
	if _, ok := body["rich_text"]; ok {
		t.Fatal("divider must not carry rich_text")
	}
}

func TestMarshalToDoAndCodeAndChildren(t *testing.T) {
	todo := marshalToMap(t, Block{Type: "to_do", RichText: []Span{{Content: "x"}}, Checked: true})
	if todo["to_do"].(map[string]any)["checked"] != true {
		t.Fatalf("checked missing: %v", todo)
	}
	code := marshalToMap(t, Block{Type: "code", RichText: []Span{{Content: "fmt.Println()"}}, Language: "go"})
	if code["code"].(map[string]any)["language"] != "go" {
		t.Fatalf("language missing: %v", code)
	}
	nested := marshalToMap(t, Block{
		Type:     "bulleted_list_item",
		RichText: []Span{{Content: "parent"}},
		Children: []Block{{Type: "bulleted_list_item", RichText: []Span{{Content: "child"}}}},
	})
	kids := nested["bulleted_list_item"].(map[string]any)["children"].([]any)
	if len(kids) != 1 {
		t.Fatalf("want 1 child, got %v", kids)
	}
}
