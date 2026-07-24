package cli

import (
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/spf13/cobra"
)

func newSetCmd() *cobra.Command {
	var wf writeFlags
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Update an existing row; fail if it does not exist",
		Long: "Update an existing row, addressed by --ticket or --page-id; fail if it\n" +
			"does not exist.\n\n" +
			"Use this when a missing row is a symptom worth surfacing rather than\n" +
			"a row to create.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			var body *service.BodyRequest
			var warnings []string
			if wf.bodyFile != "" {
				body, warnings, err = loadBody(wf.bodyFile, cmd.InOrStdin(), cmd.ErrOrStderr())
				if err != nil {
					return err
				}
			}
			var res service.Result
			// See get.go: branch on Changed, not on the value, so an empty
			// --page-id still takes the by-id path.
			if cmd.Flags().Changed("page-id") {
				res, err = svc.SetByID(cmd.Context(), wf.pageID, wf.fields(), body)
			} else {
				res, err = svc.Set(cmd.Context(), wf.fields(), body)
			}
			return emitWrite(cmd, svc.Profile().Properties, res, warnings, wf.asJSON, err)
		},
	}
	wf.bindWithPageID(cmd)
	return cmd
}
