package cli

import (
	"fmt"
	"strings"
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
	// ID is the row's board id ("BDF-271"): the identifier a person reads and
	// says out loud, as opposed to PageID's UUID. First because it is the row's
	// identity, so the JSON reads in the order the board displays a row.
	//
	// Empty both when the row carries no value and when the id role is not
	// mapped, the same rule Assignee and Priority follow below.
	ID             string `json:"id"`
	Ticket         string `json:"ticket"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	PageID         string `json:"page_id"`
	URL            string `json:"url"`
	LastEditedTime string `json:"last_edited_time"`
	// Assignee is empty both when nobody is assigned and when the role is not
	// mapped: the key is always present so a script never has to branch on it.
	Assignee string `json:"assignee"`
	// Priority is empty both when the row carries no value and when the role is
	// not mapped, the same rule Assignee follows above.
	Priority string `json:"priority"`
}

func toPageJSON(p notion.Page, props config.Properties) pageJSON {
	return pageJSON{
		ID:             p.Properties[props.ID].Text,
		Ticket:         p.Properties[props.Ticket].Text,
		Title:          p.Properties[props.Title].Text,
		Status:         p.Properties[props.Status].Text,
		PageID:         p.ID,
		URL:            p.URL,
		LastEditedTime: p.LastEditedTime.Format(time.RFC3339),
		Assignee:       p.Properties[props.Assignee].Text,
		Priority:       p.Properties[props.Priority].Text,
	}
}

func newGetCmd() *cobra.Command {
	var ticket string
	var pageID string
	var boardID string
	var asJSON bool
	var withBody bool
	var bodyOnly bool

	cmd := &cobra.Command{
		Use:   "get",
		Short: "Read the row for a ticket, a board id, or a Notion page id",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			var page notion.Page
			// Branch on Changed, not on the value: `--page-id ""` and `--id ""`
			// must still take their own path so they surface as
			// service.ErrEmptyPageID and service.ErrEmptyID rather than
			// silently falling through to a ticket lookup with an empty key
			// neither was ever given.
			switch {
			case cmd.Flags().Changed("id"):
				page, err = svc.GetByUniqueID(cmd.Context(), boardID)
			case cmd.Flags().Changed("page-id"):
				page, err = svc.GetByID(cmd.Context(), pageID)
			default:
				page, err = svc.Get(cmd.Context(), ticket)
			}
			if err != nil {
				return err
			}
			profile := svc.Profile()

			var body *notion.PageMarkdown
			if withBody || bodyOnly {
				md, err := svc.GetBody(cmd.Context(), page.ID)
				if err != nil {
					return err
				}
				body = &md
				// stderr, never stdout: --body-only is meant to be redirected into a
				// file, and a warning belongs in the terminal, not in that file.
				printWarnings(cmd.ErrOrStderr(), bodyWarnings(md))
			}

			if bodyOnly {
				// --body-only means "the body and nothing else" in both forms. With
				// --json that is the body object alone, unwrapped: degrading it to the
				// same output as --body would make the flag a no-op precisely where a
				// script relies on it.
				if asJSON {
					return printJSON(cmd.OutOrStdout(), toBodyJSON(*body))
				}
				cmd.Print(ensureTrailingNewline(body.Markdown))
				return nil
			}
			if asJSON && body != nil {
				// Only the --body form nests under "page": without it the flat
				// pageJSON shape every existing script parses must stay untouched.
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"page": toPageJSON(page, profile.Properties),
					"body": toBodyJSON(*body),
				})
			}
			if asJSON {
				// cmd.OutOrStdout(), never os.Stdout: it is what the root sets
				// and what tests can capture.
				return printJSON(cmd.OutOrStdout(), toPageJSON(page, profile.Properties))
			}
			status := page.Properties[profile.Properties.Status].Text
			priority := prioritySuffix(page, profile.Properties)
			assignee := assigneeSuffix(page, profile.Properties)
			id := idPrefix(page, profile.Properties)
			if ticketIsTitle(profile.Properties) {
				cmd.Printf("%s%s  [%s]%s%s\n  %s\n",
					id, page.Properties[profile.Properties.Title].Text, status,
					priority, assignee, page.URL)
			} else {
				cmd.Printf("%s%s  %s  [%s]%s%s\n  %s\n",
					id,
					page.Properties[profile.Properties.Ticket].Text,
					page.Properties[profile.Properties.Title].Text,
					status, priority, assignee, page.URL)
			}
			// The emptiness check is on the outside: a page with no content should add
			// nothing at all, not a blank separator line followed by nothing.
			if body != nil && body.Markdown != "" {
				cmd.Print("\n" + ensureTrailingNewline(body.Markdown))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ticket, "ticket", "", "ticket key")
	cmd.Flags().StringVar(&pageID, "page-id", "",
		"Notion page id to address directly, bypassing the ticket lookup; "+
			"accepts the full page URL copied from Notion, a bare 32-hex id, or a dashed UUID")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	cmd.Flags().StringVar(&boardID, "id", "",
		"board id of the row, as Notion shows it (e.g. TASK-271, or just 271); "+
			"needs an id property mapped in the profile")
	cmd.Flags().BoolVar(&withBody, "body", false,
		"also read the page body back, as Markdown")
	cmd.Flags().BoolVar(&bodyOnly, "body-only", false,
		"print only the page body, so redirecting to a file yields valid Markdown")
	cmd.MarkFlagsMutuallyExclusive("body", "body-only")
	cmd.MarkFlagsMutuallyExclusive("ticket", "page-id", "id")
	cmd.MarkFlagsOneRequired("ticket", "page-id", "id")
	return cmd
}

// bodyJSON is the scripting shape of a page body. Like pageJSON, treat its
// keys as public API.
type bodyJSON struct {
	Markdown string `json:"markdown"`
	// Truncated reports that Notion cut the page off at its block ceiling:
	// the Markdown is real but incomplete.
	Truncated bool `json:"truncated"`
	// UnknownBlockIDs lists blocks Notion cannot render as Markdown. Always
	// present, empty rather than null, so a script never branches on absence.
	UnknownBlockIDs []string `json:"unknown_block_ids"`
}

func toBodyJSON(md notion.PageMarkdown) bodyJSON {
	ids := md.UnknownBlockIDs
	if ids == nil {
		ids = []string{}
	}
	return bodyJSON{Markdown: md.Markdown, Truncated: md.Truncated, UnknownBlockIDs: ids}
}

// bodyWarnings turns the two lossy signals into human-readable warnings. Both
// mean "what you are reading is not the whole page", which a reader who is not
// told would never suspect.
func bodyWarnings(md notion.PageMarkdown) []string {
	var out []string
	if md.Truncated {
		out = append(out, "the page is too large to render in full: this body is truncated")
	}
	if n := len(md.UnknownBlockIDs); n > 0 {
		out = append(out, fmt.Sprintf(
			"%d block(s) have no Markdown representation and appear as <unknown/>; "+
				"they are still on the page", n))
	}
	return out
}

// ensureTrailingNewline keeps piped output well-formed without adding a blank
// line to a body that already ends in one. An empty body stays empty: a page
// with no content prints nothing rather than a stray newline.
func ensureTrailingNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
