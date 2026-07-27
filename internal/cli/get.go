package cli

import (
	"time"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/spf13/cobra"
)

// pageJSON is the stable scripting shape of a row. Renaming a key here breaks
// every script and agent that consumes it: treat it as public API.
//
// A property the profile maps to a name the row does not carry yields an empty
// string rather than an error. That is deliberate: reporting a broken mapping
// is doctor's job, and failing every read because of it would leave the user
// with no way to look at their data while they fix the config.
type pageJSON struct {
	Ticket         string `json:"ticket"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	PageID         string `json:"page_id"`
	URL            string `json:"url"`
	LastEditedTime string `json:"last_edited_time"`
	// Assignee is empty both when nobody is assigned and when the role is not
	// mapped: the key is always present so a script never has to branch on it.
	Assignee string `json:"assignee"`
}

func toPageJSON(p notion.Page, props config.Properties) pageJSON {
	return pageJSON{
		Ticket:         p.Properties[props.Ticket].Text,
		Title:          p.Properties[props.Title].Text,
		Status:         p.Properties[props.Status].Text,
		PageID:         p.ID,
		URL:            p.URL,
		LastEditedTime: p.LastEditedTime.Format(time.RFC3339),
		Assignee:       p.Properties[props.Assignee].Text,
	}
}

func newGetCmd() *cobra.Command {
	var ticket string
	var pageID string
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "get",
		Short: "Read the row for a ticket, or for a Notion page id",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			var page notion.Page
			// Branch on Changed, not on the value: `--page-id ""` must still
			// take this path so it surfaces as service.ErrEmptyPageID rather
			// than silently falling through to a ticket lookup with an empty
			// key it was never given.
			if cmd.Flags().Changed("page-id") {
				page, err = svc.GetByID(cmd.Context(), pageID)
			} else {
				page, err = svc.Get(cmd.Context(), ticket)
			}
			if err != nil {
				return err
			}
			profile := svc.Profile()
			if asJSON {
				// cmd.OutOrStdout(), never os.Stdout: it is what the root sets
				// and what tests can capture.
				return printJSON(cmd.OutOrStdout(), toPageJSON(page, profile.Properties))
			}
			status := page.Properties[profile.Properties.Status].Text
			assignee := ""
			if name := page.Properties[profile.Properties.Assignee].Text; name != "" {
				assignee = "  @" + name
			}
			if ticketIsTitle(profile.Properties) {
				cmd.Printf("%s  [%s]%s\n  %s\n",
					page.Properties[profile.Properties.Title].Text, status, assignee, page.URL)
				return nil
			}
			cmd.Printf("%s  %s  [%s]%s\n  %s\n",
				page.Properties[profile.Properties.Ticket].Text,
				page.Properties[profile.Properties.Title].Text,
				status, assignee, page.URL)
			return nil
		},
	}
	cmd.Flags().StringVar(&ticket, "ticket", "", "ticket key")
	cmd.Flags().StringVar(&pageID, "page-id", "",
		"Notion page id to address directly, bypassing the ticket lookup; "+
			"accepts the full page URL copied from Notion, a bare 32-hex id, or a dashed UUID")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	cmd.MarkFlagsMutuallyExclusive("ticket", "page-id")
	cmd.MarkFlagsOneRequired("ticket", "page-id")
	return cmd
}
