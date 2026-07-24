// Package manifest parses the bulk file `notion-track apply` works from: a
// list of writes to perform, one per entry, in JSON or CSV.
//
// Parsing is pure and file-format-agnostic once the bytes are in hand, so the
// rules that matter — which operations exist, which fields are required, what
// counts as a typo — are stated once and tested without a filesystem.
package manifest

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Op is what to do with one entry.
const (
	OpUpsert = "upsert"
	OpSet    = "set"
)

// Entry is one write.
type Entry struct {
	// Op is "upsert" (default) or "set". Defaulting to upsert is deliberate:
	// it is the idempotent one, so a manifest run twice by mistake leaves the
	// board as it was.
	Op     string `json:"op,omitempty"`
	Ticket string `json:"ticket"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
	Due    string `json:"due,omitempty"`
	// BodyFile is a path to a Markdown body, resolved relative to the manifest
	// rather than to the working directory, so a manifest and the files it
	// names travel together.
	BodyFile string `json:"body_file,omitempty"`

	// Index is the entry's 1-based position, for error messages. It is not
	// part of the file format.
	Index int `json:"-"`
}

// fieldNames are the CSV columns and JSON keys an entry may carry. Anything
// else is a typo: silently ignoring an unknown column is how "stuats" ends up
// leaving every row's status unset with no warning.
var fieldNames = []string{"op", "ticket", "title", "status", "due", "body_file"}

// Parse reads a manifest, choosing the format from path's extension.
func Parse(path string, data []byte) ([]Entry, error) {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".json":
		return parseJSON(data)
	case ".csv":
		return parseCSV(data)
	default:
		return nil, fmt.Errorf(
			"cannot tell the format of %s from its extension %q; use .json or .csv", path, ext)
	}
}

func parseJSON(data []byte) ([]Entry, error) {
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	entries := make([]Entry, 0, len(raw))
	for i, obj := range raw {
		entry := Entry{Index: i + 1}
		// Read through the raw map rather than unmarshalling into the struct
		// directly: encoding/json ignores unknown keys, and an ignored key is
		// exactly the mistake worth catching.
		for key, value := range obj {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("entry %d: %q must be a string", i+1, key)
			}
			if err := assign(&entry, key, text); err != nil {
				return nil, fmt.Errorf("entry %d: %w", i+1, err)
			}
		}
		if err := validate(&entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseCSV(data []byte) ([]Entry, error) {
	r := csv.NewReader(strings.NewReader(string(data)))
	// Ragged rows are a mistake, not a shorthand: a row missing its last field
	// would otherwise silently drop whatever that column meant.
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("invalid CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("the manifest is empty")
	}

	header := records[0]
	for i, name := range header {
		header[i] = strings.TrimSpace(strings.ToLower(name))
	}

	entries := make([]Entry, 0, len(records)-1)
	for row, record := range records[1:] {
		entry := Entry{Index: row + 1}
		for col, value := range record {
			if err := assign(&entry, header[col], strings.TrimSpace(value)); err != nil {
				return nil, fmt.Errorf("entry %d: %w", row+1, err)
			}
		}
		if err := validate(&entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func assign(e *Entry, field, value string) error {
	switch field {
	case "op":
		e.Op = value
	case "ticket":
		e.Ticket = value
	case "title":
		e.Title = value
	case "status":
		e.Status = value
	case "due":
		e.Due = value
	case "body_file":
		e.BodyFile = value
	default:
		return fmt.Errorf("unknown field %q; known fields: %s", field, strings.Join(sorted(), ", "))
	}
	return nil
}

func validate(e *Entry) error {
	if e.Op == "" {
		e.Op = OpUpsert
	}
	if e.Op != OpUpsert && e.Op != OpSet {
		return fmt.Errorf("entry %d: unknown op %q; use %q or %q", e.Index, e.Op, OpUpsert, OpSet)
	}
	if e.Ticket == "" {
		// The ticket key is the row's identity on both paths: without it there
		// is nothing to look up and nothing to create that could be found
		// again.
		return fmt.Errorf("entry %d: a ticket is required", e.Index)
	}
	return nil
}

func sorted() []string {
	names := append([]string(nil), fieldNames...)
	sort.Strings(names)
	return names
}
