package cli

import (
	"github.com/spf13/cobra"
)

func newSetCmd() *cobra.Command {
	var wf writeFlags
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Update an existing row; fail if the ticket does not exist",
		Long: "Update an existing row; fail if the ticket does not exist.\n\n" +
			"Use this when a missing ticket is a symptom worth surfacing rather than\n" +
			"a row to create.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			res, err := svc.Set(cmd.Context(), wf.fields())
			if err != nil {
				return err
			}
			if wf.asJSON {
				out := toPageJSON(res.Page, svc.Profile().Properties)
				return printJSON(cmd.OutOrStdout(), map[string]any{"action": res.Action, "page": out})
			}
			return nil
		},
	}
	wf.bind(cmd)
	return cmd
}
