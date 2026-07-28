package service

import (
	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

// Plan is what a write would have done, produced instead of doing it when the
// service is in dry-run mode.
//
// "Without calling the API" can only mean without *writing*: whether a ticket
// key resolves to a create or an update, and whether a status value even
// exists, are questions only the live data source can answer. A dry run
// therefore reads — the same query and schema fetch the real run makes — and
// stops before the first write.
type Plan struct {
	Action string `json:"action"` // "created" or "updated", as Result.Action
	// PageID is the row that would be updated, empty for a create.
	PageID string `json:"page_id,omitempty"`
	URL    string `json:"url,omitempty"`
	// Properties are the columns that would be written, by their real names in
	// the data source rather than by role: what the user will see change in
	// Notion, not what notion-track calls it internally.
	Properties []PlannedProperty `json:"properties"`
	// BodyBlocks is how many blocks --body-file would replace the body with.
	BodyBlocks int `json:"body_blocks,omitempty"`
	// Cleared names the columns a write would empty. Properties cannot carry
	// them: it reports what would be *written*, and skips empty values by
	// design, which would make a clear invisible in the one command that exists
	// to make writes visible.
	Cleared []string `json:"cleared,omitempty"`
}

// PlannedProperty is one column a write would set.
type PlannedProperty struct {
	Column string `json:"column"`
	Value  string `json:"value"`
}

// planFor describes the write that f would perform, in the vocabulary of the
// data source.
//
// It reports only the fields actually supplied. An omitted field means "leave
// this alone" everywhere else in notion-track, and listing it here as if it
// were about to be written would misrepresent the very thing a dry run exists
// to show.
func planFor(action, pageID, url string, f tracker.Fields, props config.Properties, bodyBlocks int) *Plan {
	plan := &Plan{Action: action, PageID: pageID, URL: url, BodyBlocks: bodyBlocks}
	for _, p := range []PlannedProperty{
		{Column: props.Ticket, Value: f.Ticket},
		{Column: props.Title, Value: f.Title},
		{Column: props.Status, Value: f.Status},
		{Column: props.Due, Value: f.Due},
		{Column: props.Assignee, Value: f.Assignee},
		{Column: props.Priority, Value: f.Priority},
	} {
		// An unmapped role has no column to name, and a field left off the
		// command line has no value to write.
		if p.Column != "" && p.Value != "" {
			plan.Properties = append(plan.Properties, p)
		}
	}
	if f.Unassign && props.Assignee != "" {
		plan.Cleared = append(plan.Cleared, props.Assignee)
	}
	return plan
}
