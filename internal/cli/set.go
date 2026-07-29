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
		Long: "Update an existing row, addressed by --ticket, --id or --page-id; fail\n" +
			"if it does not exist.\n\n" +
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
				body, warnings, err = loadBody(wf.bodyFile, cmd.InOrStdin(), cmd.ErrOrStderr(), wf.bodyVars())
				if err != nil {
					return err
				}
			}
			var res service.Result
			// See get.go: branch on Changed, not on the value, so an empty
			// --page-id or --id still takes its own path.
			switch {
			case cmd.Flags().Changed("id"):
				res, err = svc.DryRun(wf.dryRun).SetByUniqueID(cmd.Context(), wf.boardID, wf.fields(), body)
			case cmd.Flags().Changed("page-id"):
				res, err = svc.DryRun(wf.dryRun).SetByID(cmd.Context(), wf.pageID, wf.fields(), body)
			default:
				res, err = svc.DryRun(wf.dryRun).Set(cmd.Context(), wf.fields(), body)
			}
			return emitWrite(cmd, svc.Profile().Properties, res, warnings, wf.asJSON, err)
		},
	}
	wf.bindWithPageID(cmd)
	return cmd
}
