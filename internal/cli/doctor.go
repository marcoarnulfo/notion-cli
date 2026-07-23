package cli

import (
	"fmt"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check token, data source access, property mapping and duplicates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			checks := svc.Doctor(cmd.Context())
			annotateTokenSource(checks)

			if asJSON {
				if err := printJSON(cmd.OutOrStdout(), checks); err != nil {
					return err
				}
			} else {
				for _, c := range checks {
					symbol := map[string]string{"ok": "✓", "warn": "!", "fail": "✗"}[c.Status]
					cmd.Printf("%s %-14s %s\n", symbol, c.Name, c.Detail)
				}
			}

			var failed []string
			for _, c := range checks {
				if c.Status == "fail" {
					failed = append(failed, c.Name)
				}
			}
			switch {
			case len(failed) == 0:
				return nil
			// Every other command exits ExitAuth on an invalid token;
			// Service.Doctor stops at the token check and returns just that
			// one Check when it fails, so "token is the only failed check"
			// is exactly "the token check failed" here. Matching that code
			// makes doctor consistent with the rest of the CLI instead of
			// being the one command that reports a bad token as a generic
			// failure.
			case len(failed) == 1 && failed[0] == "token":
				return Errorf(ExitAuth, "authentication failed")
			default:
				return Errorf(ExitError, "one or more checks failed")
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	return cmd
}

// annotateTokenSource prefixes the "token" check's detail with where the
// winning token came from. A user with a different token in NOTION_TOKEN
// than in credentials.yml has no other way to tell which one this run
// actually used.
func annotateTokenSource(checks []service.Check) {
	_, source, err := config.LoadToken()
	if err != nil || source == "" {
		return
	}

	desc := "environment"
	if source == "file" {
		path, err := config.CredentialsPath()
		if err != nil {
			return
		}
		desc = path
	}

	for i, c := range checks {
		if c.Name == "token" {
			checks[i].Detail = fmt.Sprintf("token from %s\n  %s", desc, c.Detail)
			return
		}
	}
}
