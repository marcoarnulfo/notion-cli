package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/marcoarnulfo/notion-cli/internal/manifest"
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

// maxManifestBytes caps the manifest the same way --body-file is capped: a
// list of writes this large is a mistake, and rejecting it up front beats
// discovering it halfway through applying.
const maxManifestBytes = 1 << 20

// applyOutcome is what happened to one entry.
type applyOutcome struct {
	Index  int           `json:"index"`
	Op     string        `json:"op"`
	Ticket string        `json:"ticket"`
	Action string        `json:"action,omitempty"` // created | updated
	Plan   *service.Plan `json:"plan,omitempty"`   // --dry-run only
	Error  string        `json:"error,omitempty"`

	// err is the failure itself, kept so the command can exit with its code
	// while the report only ever prints the message.
	err error
}

func (o applyOutcome) fail(err error) applyOutcome {
	o.Error = err.Error()
	o.err = err
	return o
}

const applyExample = `  # tasks.json
  [
    {"op": "upsert", "ticket": "BDF-1", "title": "Hardening", "status": "In progress", "assignee": "mirko"},
    {"op": "set", "ticket": "BDF-2", "status": "Done", "unassign": true}
  ]

  # tasks.csv
  op,ticket,title,status,due,body_file,assignee,unassign
  upsert,BDF-1,Hardening,In progress,2026-08-01,notes.md,mirko,
  set,BDF-2,,Done,,,,true`

func newApplyCmd() *cobra.Command {
	var (
		file   string
		asJSON bool
		expand bool
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply many writes from a JSON or CSV manifest",
		Long: "Apply many writes from a JSON or CSV manifest, one entry at a time.\n\n" +
			"The format is chosen from the file's extension. Every entry needs a\n" +
			"ticket; op defaults to upsert. body_file paths are resolved relative\n" +
			"to the manifest, so a manifest and the files it names travel together.\n\n" +
			"apply stops at the first entry that fails and reports what was and was\n" +
			"not applied, so a half-finished run is something you can see rather\n" +
			"than something you have to reconstruct.",
		Example: applyExample,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			entries, err := readManifest(file)
			if err != nil {
				return err
			}

			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			svc.DryRun(dryRun)

			// In order, one at a time, never concurrently: two writes racing
			// on the same ticket key can create a duplicate (see the README's
			// limitations), and a manifest is exactly where the same key is
			// most likely to appear twice.
			outcomes := make([]applyOutcome, 0, len(entries))
			var failure error
			for _, entry := range entries {
				outcome := applyOne(cmd, svc, entry, filepath.Dir(file), expand)
				outcomes = append(outcomes, outcome)
				if outcome.err != nil {
					failure = outcome.err
					break
				}
			}

			return reportApply(cmd, outcomes, len(entries), asJSON, failure)
		},
	}

	cmd.Flags().StringVar(&file, "file", "", "manifest to apply, .json or .csv (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	cmd.Flags().BoolVar(&expand, "expand", false,
		"expand {{ticket}} and {{date}} placeholders in each entry's body file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"report what each entry would write, and write nothing")
	cmd.MarkFlagRequired("file")
	return cmd
}

func readManifest(path string) ([]manifest.Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, Errorf(ExitUsage, "reading manifest %s: %v", path, err)
	}
	if info.Size() > maxManifestBytes {
		return nil, Errorf(ExitUsage, "manifest %s is over the %d-byte limit", path, maxManifestBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, Errorf(ExitUsage, "reading manifest %s: %v", path, err)
	}
	entries, err := manifest.Parse(path, data)
	if err != nil {
		// Every manifest problem is a usage error: here, the file is the
		// invocation.
		return nil, Errorf(ExitUsage, "%v", err)
	}
	return entries, nil
}

func applyOne(cmd *cobra.Command, svc *service.Service, entry manifest.Entry, dir string, expand bool) applyOutcome {
	out := applyOutcome{Index: entry.Index, Op: entry.Op, Ticket: entry.Ticket}

	var body *service.BodyRequest
	if entry.BodyFile != "" {
		var vars map[string]string
		if expand {
			vars = map[string]string{"ticket": entry.Ticket, "date": now().Format("2006-01-02")}
		}
		path := entry.BodyFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		req, warnings, err := loadBody(path, cmd.InOrStdin(), cmd.ErrOrStderr(), vars)
		if err != nil {
			return out.fail(err)
		}
		printWarnings(cmd.ErrOrStderr(), warnings)
		body = req
	}

	fields := tracker.Fields{
		Ticket: entry.Ticket, Title: entry.Title, Status: entry.Status, Due: entry.Due,
		Assignee: entry.Assignee, Unassign: entry.Unassign,
	}

	var res service.Result
	var err error
	if entry.Op == manifest.OpSet {
		res, err = svc.Set(cmd.Context(), fields, body)
	} else {
		res, err = svc.Upsert(cmd.Context(), fields, body)
	}
	if err != nil {
		return out.fail(err)
	}
	out.Action = res.Action
	out.Plan = res.Plan
	return out
}

func reportApply(cmd *cobra.Command, outcomes []applyOutcome, total int, asJSON bool, failure error) error {
	applied := 0
	for _, o := range outcomes {
		if o.err == nil {
			applied++
		}
	}

	if asJSON {
		if err := printJSON(cmd.OutOrStdout(), map[string]any{
			"applied": applied,
			"total":   total,
			"entries": outcomes,
		}); err != nil {
			return err
		}
	} else {
		for _, o := range outcomes {
			switch {
			case o.err != nil:
				cmd.Printf("%d/%d %s %s failed: %s\n", o.Index, total, o.Op, o.Ticket, o.Error)
			case o.Plan != nil:
				cmd.Printf("%d/%d %s %s would be %s\n", o.Index, total, o.Op, o.Ticket, o.Plan.Action)
			default:
				cmd.Printf("%d/%d %s %s %s\n", o.Index, total, o.Op, o.Ticket, o.Action)
			}
		}
		if failure != nil {
			// Where to resume from is the whole point of stopping rather than
			// plowing on. On stderr, so the per-entry lines on stdout stay a
			// clean record of what happened.
			cmd.PrintErrf("stopped at entry %d of %d: %d applied, %d not applied\n",
				outcomes[len(outcomes)-1].Index, total, applied, total-applied)
		}
	}

	// The failing entry's own error, so a pipeline branching on exit code 3
	// (not found) or 4 (duplicate) still learns why the run stopped.
	return failure
}
