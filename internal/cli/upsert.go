package cli

import (
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
	"github.com/spf13/cobra"
)

// writeFlags are the fields upsert and set share.
type writeFlags struct {
	ticket   string
	pageID   string
	title    string
	status   string
	due      string
	asJSON   bool
	bodyFile string
}

// bindShared registers the flags that carry no addressing semantics, common
// to every write command's binding.
func (wf *writeFlags) bindShared(cmd *cobra.Command) {
	cmd.Flags().StringVar(&wf.title, "title", "", "title to set")
	cmd.Flags().StringVar(&wf.status, "status", "", "status to set")
	cmd.Flags().StringVar(&wf.due, "due", "", "due date, YYYY-MM-DD")
	cmd.Flags().BoolVar(&wf.asJSON, "json", false, "print machine-readable JSON")
	cmd.Flags().StringVar(&wf.bodyFile, "body-file", "",
		"Markdown file whose content replaces the page body ('-' for stdin); replace semantics, owns the body")
}

// bind is upsert's binding: a page id cannot exist before the row does, so
// --ticket is the only way to address one.
func (wf *writeFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&wf.ticket, "ticket", "", "ticket key (required)")
	wf.bindShared(cmd)
	cmd.MarkFlagRequired("ticket")
}

// bindWithPageID is set's binding: --ticket and --page-id are alternate,
// mutually exclusive ways to address an existing row, and exactly one of
// them is required.
func (wf *writeFlags) bindWithPageID(cmd *cobra.Command) {
	cmd.Flags().StringVar(&wf.ticket, "ticket", "", "ticket key")
	cmd.Flags().StringVar(&wf.pageID, "page-id", "",
		"Notion page id to address directly, bypassing the ticket lookup; "+
			"accepts the full page URL copied from Notion, a bare 32-hex id, or a dashed UUID")
	wf.bindShared(cmd)
	cmd.MarkFlagsMutuallyExclusive("ticket", "page-id")
	cmd.MarkFlagsOneRequired("ticket", "page-id")
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
			var body *service.BodyRequest
			var warnings []string
			if wf.bodyFile != "" {
				body, warnings, err = loadBody(wf.bodyFile, cmd.InOrStdin(), cmd.ErrOrStderr())
				if err != nil {
					return err
				}
			}
			res, err := svc.Upsert(cmd.Context(), wf.fields(), body)
			return emitWrite(cmd, svc.Profile().Properties, res, warnings, wf.asJSON, err)
		},
	}
	wf.bind(cmd)
	return cmd
}
