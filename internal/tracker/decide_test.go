package tracker

import (
	"errors"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

func TestDecide(t *testing.T) {
	tests := []struct {
		name       string
		matches    []notion.Page
		wantAction Action
		wantPageID string
		wantErr    bool
	}{
		{
			name:       "no match creates",
			matches:    nil,
			wantAction: ActionCreate,
		},
		{
			name:       "one match updates that page",
			matches:    []notion.Page{{ID: "page1"}},
			wantAction: ActionUpdate,
			wantPageID: "page1",
		},
		{
			name: "several matches is a data problem, not a choice",
			matches: []notion.Page{
				{ID: "page1", URL: "https://notion.so/page1"},
				{ID: "page2", URL: "https://notion.so/page2"},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decide("BDF-231", tc.matches)
			if tc.wantErr {
				var dup *DuplicateError
				if !errors.As(err, &dup) {
					t.Fatalf("got %v, want *DuplicateError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if got.Action != tc.wantAction || got.PageID != tc.wantPageID {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

// The whole point of failing on duplicates is that the user can go fix them,
// so the error has to hand over the URLs.
func TestDuplicateErrorListsPageURLs(t *testing.T) {
	err := &DuplicateError{
		Ticket: "BDF-231",
		Pages: []notion.Page{
			{ID: "page1", URL: "https://notion.so/page1"},
			{ID: "page2", URL: "https://notion.so/page2"},
		},
	}
	msg := err.Error()
	for _, want := range []string{"BDF-231", "https://notion.so/page1", "https://notion.so/page2"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message is missing %q: %s", want, msg)
		}
	}
}
