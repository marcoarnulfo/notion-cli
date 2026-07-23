package cli

import (
	"github.com/spf13/cobra"
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
			for _, p := range pages {
				cmd.Printf("%-20s %-40s [%s]\n",
					p.Properties[profile.Properties.Ticket].Text,
					p.Properties[profile.Properties.Title].Text,
					p.Properties[profile.Properties.Status].Text)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status value")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	return cmd
}
