package cli

import (
	"github.com/spf13/cobra"
)

// The merged column is exactly as wide as the two it replaces (20 + the
// separating space + 40), so the status lands in the same screen column
// whether or not the ticket and the title share a property.
const (
	listRowFormat       = "%-20s %-40s [%s]\n"
	listMergedRowFormat = "%-61s [%s]\n"
)

func newListCmd() *cobra.Command {
	var status string
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List rows, optionally filtered by status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			pages, err := svc.List(cmd.Context(), status)
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
				if merged {
					cmd.Printf(listMergedRowFormat, p.Properties[profile.Properties.Title].Text, status)
					continue
				}
				cmd.Printf(listRowFormat,
					p.Properties[profile.Properties.Ticket].Text,
					p.Properties[profile.Properties.Title].Text,
					status)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status value")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	return cmd
}
