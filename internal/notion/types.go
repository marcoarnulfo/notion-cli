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
// cares about: its name, its type, the options a select or status accepts,
// and the prefix a unique_id column carries.
type Property struct {
	Name    string
	Type    string
	Options []string
	// Prefix is the string Notion prepends to a unique_id column's numbers
	// ("BDF" in "BDF-271"). Empty for every other property type, and also for
	// a unique_id column configured without one.
	Prefix string
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
	// DataSourceID is the id of the data source this page belongs to, read
	// from parent.data_source_id. It is empty when the page's parent is not
	// a data source (e.g. a plain sub-page), which callers that check it
	// must treat as "unknown" rather than "mismatch".
	DataSourceID string
}

// Filter is a raw Notion query filter.
type Filter map[string]any
