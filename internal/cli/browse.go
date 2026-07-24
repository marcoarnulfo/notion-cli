package cli

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
	"github.com/marcoarnulfo/notion-cli/internal/tui"
)

// runBrowser is the seam that keeps bubbletea's runtime out of the tests, the
// same way runWizard does for init. The model itself is tested in internal/tui.
var runBrowser = func(m tui.BrowseModel) error {
	// The alternate screen buffer, unlike the wizard: this one takes over the
	// whole terminal for as long as it runs, and the user should get their
	// scrollback back untouched when they quit.
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// runBrowse is `notion-track` with no arguments at a terminal.
func runBrowse(cmd *cobra.Command) error {
	svc, err := buildService(cmd)
	if err != nil {
		return err
	}
	return runBrowser(tui.NewBrowser(boardAdapter{ctx: cmd.Context(), svc: svc}))
}

// boardAdapter is the whole of what the browsing TUI knows about Notion: it
// turns the service layer into tui.Board, resolving the profile's property
// mapping once here so no screen has to.
//
// Holding a context in a struct is normally a smell. Here it is deliberate:
// tui.Board's methods are called from bubbletea commands that have no context
// to pass, and the one being held is the command's own, which lives exactly as
// long as the program does.
type boardAdapter struct {
	ctx context.Context
	svc *service.Service
}

func (b boardAdapter) List(status string) ([]tui.Row, error) {
	pages, err := b.svc.List(b.ctx, status)
	if err != nil {
		return nil, err
	}
	props := b.svc.Profile().Properties
	rows := make([]tui.Row, 0, len(pages))
	for _, p := range pages {
		rows = append(rows, tui.Row{
			PageID: p.ID,
			Ticket: p.Properties[props.Ticket].Text,
			Title:  p.Properties[props.Title].Text,
			Status: p.Properties[props.Status].Text,
			// Date, not Text: a date property carries its value in its own
			// field. An unmapped due role reads the zero value and yields "",
			// which the browser renders as a dash.
			Due: p.Properties[props.Due].Date,
			URL: p.URL,
		})
	}
	return rows, nil
}

// SetStatus writes by page id rather than by ticket key: the row is already in
// hand, and a ticket key is not guaranteed unique — going back through a
// lookup could fail on a duplicate the user can plainly see on screen.
func (b boardAdapter) SetStatus(pageID, status string) error {
	_, err := b.svc.SetByID(b.ctx, pageID, tracker.Fields{Status: status}, nil)
	return err
}

func (b boardAdapter) Create(ticket, title, status string) error {
	_, err := b.svc.Upsert(b.ctx, tracker.Fields{Ticket: ticket, Title: title, Status: status}, nil)
	return err
}

func (b boardAdapter) Statuses() ([]string, error) {
	schema, err := b.svc.Schema(b.ctx)
	if err != nil {
		return nil, err
	}
	name := b.svc.Profile().Properties.Status
	prop, ok := schema.Properties[name]
	if !ok {
		return nil, fmt.Errorf(
			"status property %q does not exist in the data source; run 'notion-track doctor'", name)
	}
	return prop.Options, nil
}
