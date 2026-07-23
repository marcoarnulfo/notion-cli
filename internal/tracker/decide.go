// Package tracker holds notion-track's domain logic.
//
// Nothing here performs I/O: it takes data in and returns decisions. That is
// what makes the interesting behaviour — upsert semantics, property payloads,
// status validation — testable without a network or a mock.
package tracker

import (
	"fmt"
	"strings"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

// Action is what an upsert resolved to.
type Action int

const (
	// ActionCreate means no row carries this ticket key yet.
	ActionCreate Action = iota
	// ActionUpdate means exactly one row does.
	ActionUpdate
)

// Decision is the outcome of Decide.
type Decision struct {
	Action Action
	PageID string // set only for ActionUpdate
}

// DuplicateError reports several rows sharing one ticket key.
type DuplicateError struct {
	Ticket string
	Pages  []notion.Page
}

func (e *DuplicateError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ticket %q matches %d rows; refusing to guess which one to update:",
		e.Ticket, len(e.Pages))
	for _, p := range e.Pages {
		// URL is what makes this message actionable, so fall back to the id
		// rather than printing a blank line the user cannot act on.
		if p.URL != "" {
			fmt.Fprintf(&b, "\n  %s", p.URL)
			continue
		}
		fmt.Fprintf(&b, "\n  page %s (no url)", p.ID)
	}
	b.WriteString("\n  fix: delete the duplicates in Notion, then run the command again")
	return b.String()
}

// Decide turns the rows matching a ticket key into a create-or-update choice.
//
// More than one match is refused rather than resolved: duplicates are a data
// problem, and silently updating "the most recent" one would hide it and let
// the other row drift forever.
func Decide(ticket string, matches []notion.Page) (Decision, error) {
	switch len(matches) {
	case 0:
		return Decision{Action: ActionCreate}, nil
	case 1:
		return Decision{Action: ActionUpdate, PageID: matches[0].ID}, nil
	default:
		return Decision{}, &DuplicateError{Ticket: ticket, Pages: matches}
	}
}
