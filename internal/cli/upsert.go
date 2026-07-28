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
	assignee string
	unassign bool
	priority string
	asJSON   bool
	bodyFile string
	expand   bool
	dryRun   bool
}

// bindShared registers the flags that carry no addressing semantics, common
// to every write command's binding.
func (wf *writeFlags) bindShared(cmd *cobra.Command) {
	cmd.Flags().StringVar(&wf.title, "title", "", "title to set")
	cmd.Flags().StringVar(&wf.status, "status", "", "status to set")
	cmd.Flags().StringVar(&wf.due, "due", "", "due date, YYYY-MM-DD")
	cmd.Flags().StringVar(&wf.assignee, "assignee", "",
		"who the row belongs to; a partial name is enough when it is unambiguous, "+
			"and 'me' stands for NOTION_TRACK_ME")
	cmd.Flags().BoolVar(&wf.unassign, "unassign", false, "clear the assignee")
	cmd.MarkFlagsMutuallyExclusive("assignee", "unassign")
	cmd.Flags().StringVar(&wf.priority, "priority", "",
		"how urgent the row is; a partial value is enough when it is unambiguous")
	cmd.Flags().BoolVar(&wf.asJSON, "json", false, "print machine-readable JSON")
	cmd.Flags().StringVar(&wf.bodyFile, "body-file", "",
		"Markdown file whose content replaces the page body ('-' for stdin); replace semantics, owns the body")
	cmd.Flags().BoolVar(&wf.expand, "expand", false,
		"expand {{ticket}} and {{date}} placeholders in --body-file before sending it")
	cmd.Flags().BoolVar(&wf.dryRun, "dry-run", false,
		"report what would be written, and write nothing")

	// A PreRunE rather than a check inside each RunE: bindShared is the one
	// place both write commands pass through, and duplicating the guard is how
	// one of the two eventually loses it.
	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed("assignee") && wf.assignee == "" {
			return service.ErrEmptyAssignee
		}
		return nil
	}
}

// bodyVars are the placeholder values for --expand, or nil when the flag is
// off. Off by default on purpose: --body-file shipped before this existed, and
// a body that legitimately contains {{...}} — a document about templating, a
// snippet of Handlebars — must keep working exactly as it did.
//
// ticket is offered even when the row was addressed by --page-id, where it is
// empty: a body written for either addressing form should not fail depending
// on which one the caller used.
func (wf *writeFlags) bodyVars() map[string]string {
	if !wf.expand {
		return nil
	}
	return map[string]string{
		"ticket": wf.ticket,
		"date":   now().Format("2006-01-02"),
	}
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
	return tracker.Fields{
		Ticket: wf.ticket, Title: wf.title, Status: wf.status, Due: wf.due,
		Assignee: wf.assignee, Unassign: wf.unassign, Priority: wf.priority,
	}
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
				body, warnings, err = loadBody(wf.bodyFile, cmd.InOrStdin(), cmd.ErrOrStderr(), wf.bodyVars())
				if err != nil {
					return err
				}
			}
			res, err := svc.DryRun(wf.dryRun).Upsert(cmd.Context(), wf.fields(), body)
			return emitWrite(cmd, svc.Profile().Properties, res, warnings, wf.asJSON, err)
		},
	}
	wf.bind(cmd)
	return cmd
}
