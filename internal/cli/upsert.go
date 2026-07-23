package cli

import (
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
	"github.com/spf13/cobra"
)

// writeFlags are the fields upsert and set share.
type writeFlags struct {
	ticket string
	title  string
	status string
	due    string
	asJSON bool
}

func (wf *writeFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&wf.ticket, "ticket", "", "ticket key (required)")
	cmd.Flags().StringVar(&wf.title, "title", "", "title to set")
	cmd.Flags().StringVar(&wf.status, "status", "", "status to set")
	cmd.Flags().StringVar(&wf.due, "due", "", "due date, YYYY-MM-DD")
	cmd.Flags().BoolVar(&wf.asJSON, "json", false, "print machine-readable JSON")
	cmd.MarkFlagRequired("ticket")
}

func (wf *writeFlags) fields() tracker.Fields {
	return tracker.Fields{Ticket: wf.ticket, Title: wf.title, Status: wf.status, Due: wf.due}
}

func newUpsertCmd() *cobra.Command {
	var wf writeFlags
	cmd := &cobra.Command{
		Use:   "upsert",
		Short: "Create the row for a ticket, or update it if it already exists",
		Long: "Create the row for a ticket, or update it if it already exists.\n\n" +
			"Running it twice yields one row, which is what makes it safe in a retried CI job.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			res, err := svc.Upsert(cmd.Context(), wf.fields())
			if err != nil {
				return err
			}
			if wf.asJSON {
				out := toPageJSON(res.Page, svc.Profile().Properties)
				return printJSON(cmd.OutOrStdout(), map[string]any{"action": res.Action, "page": out})
			}
			return nil // quiet on success
		},
	}
	wf.bind(cmd)
	return cmd
}
