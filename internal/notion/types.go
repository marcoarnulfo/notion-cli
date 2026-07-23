package notion

import "time"

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

// Property is one column of a data source, flattened to what notion-track
// cares about: its name, its type, and the options a select or status accepts.
type Property struct {
	Name    string
	Type    string
	Options []string
}

// Schema is the property set of a data source.
type Schema struct {
	DataSourceID string
	Title        string
	Properties   map[string]Property
}

// PropertyValue is a property read off a page, flattened to the shapes
// notion-track needs. Text carries title, rich_text, select and status alike.
type PropertyValue struct {
	Type     string
	Text     string
	Date     string
	Checkbox bool
}

// Page is one row of a data source.
type Page struct {
	ID             string
	URL            string
	LastEditedTime time.Time
	Properties     map[string]PropertyValue
}

// Filter is a raw Notion query filter.
type Filter map[string]any
