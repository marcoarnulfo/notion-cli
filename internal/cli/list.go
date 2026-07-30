package cli

import (
	"github.com/spf13/cobra"

	"github.com/marcoarnulfo/notion-cli/internal/service"
)

// The merged column is exactly as wide as the two it replaces (20 + the
// separating space + 40), so the status lands in the same screen column
// whether or not the ticket and the title share a property. Priority and
// assignee are appended as trailing %s segments, each empty when there is
// nothing to show, keeping the existing columns byte-identical for profiles
// without the role.
//
// The id leads as a %s segment of its own, ahead of the ticket/title columns:
// it is the row's name, and names read down the left edge of a list. Empty
// when the role is unmapped, the same rule the two trailing suffixes follow,
// so a profile without the id role prints byte-identical columns to before.
const (
	listRowFormat       = "%s%-20s %-40s [%s]%s%s\n"
	listMergedRowFormat = "%s%-61s [%s]%s%s\n"
)

func newListCmd() *cobra.Command {
	var status string
	var assignee string
	var unassigned bool
	var priority string
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List rows, optionally filtered by status, assignee or priority",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			pages, err := svc.List(cmd.Context(), service.ListFilter{
				Status: status, Assignee: assignee, Unassigned: unassigned, Priority: priority,
			})
			if err != nil {
				return err
			}
			profile := svc.Profile()

			if asJSON {
				rows := make([]pageJSON, 0, len(pages))
				for _, p := range pages {
					rows = append(rows, toPageJSON(p, profile.Properties))
				}
				return printJSON(cmd.OutOrStdout(), rows)
			}
			// Printing nothing is ambiguous at a terminal: it reads the same
			// as a command that silently failed. The notice goes to stderr so
			// stdout carries rows and only rows — `list | wc -l` must not
			// count this line. --json already answers the question with [],
			// and a script parsing it must not find prose on either stream,
			// so this is deliberately below the --json branch.
			if len(pages) == 0 {
				cmd.PrintErrln("no matching tasks")
				return nil
			}

			merged := ticketIsTitle(profile.Properties)
			for _, p := range pages {
				status := p.Properties[profile.Properties.Status].Text
				priority := prioritySuffix(p, profile.Properties)
				assignee := assigneeSuffix(p, profile.Properties)
				id := idPrefix(p, profile.Properties)
				if merged {
					cmd.Printf(listMergedRowFormat, id,
						p.Properties[profile.Properties.Title].Text, status, priority, assignee)
					continue
				}
				cmd.Printf(listRowFormat, id,
					p.Properties[profile.Properties.Ticket].Text,
					p.Properties[profile.Properties.Title].Text,
					status, priority, assignee)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status value")
	cmd.Flags().StringVar(&assignee, "assignee", "",
		"only rows assigned to this person; a partial name is enough, and 'me' stands for your configured identity")
	cmd.Flags().BoolVar(&unassigned, "unassigned", false, "only rows with no assignee")
	cmd.MarkFlagsMutuallyExclusive("assignee", "unassigned")
	cmd.Flags().StringVar(&priority, "priority", "",
		"only rows with this priority; a partial value is enough when it is unambiguous")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	return cmd
}
