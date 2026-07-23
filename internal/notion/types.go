package notion

// RichText is the fragment shape Notion uses for titles and text values.
type RichText struct {
	PlainText string `json:"plain_text,omitempty"`
	Text      *Text  `json:"text,omitempty"`
}

// Text is the writable half of a rich text fragment.
type Text struct {
	Content string `json:"content"`
}

// PlainText flattens rich text fragments into a single string.
func PlainText(rt []RichText) string {
	var s string
	for _, r := range rt {
		s += r.PlainText
	}
	return s
}

// DataSourceRef identifies one data source and the database holding it.
//
// A database may expose several data sources, all carrying the database's
// title, so the id is what disambiguates them in `init --list`.
type DataSourceRef struct {
	ID         string
	Title      string
	DatabaseID string
}
