package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/secrets"
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
			// Appended here rather than inside Service.Doctor: this one asks
			// nothing of Notion, and Doctor returns early with just the token
			// check when authentication fails — exactly the run where a
			// committed token is most worth mentioning.
			checks = append(checks, checkTrackedSecrets(cmd.Context()))

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

// checkTrackedSecrets warns when a file the current repository tracks carries
// something shaped like an integration token.
//
// It warns and never fails: a committed token does not stop notion-track from
// working, and taking the exit code to non-zero would break the CI pipelines
// that run doctor as a gate — over a file whose contents this command cannot
// fix anyway.
//
// Anything that stops the scan from running (no repository, no git, an
// unreadable tree) reports "ok" with the reason. A user checking their setup
// from their home directory has nothing to fix, and a warning they cannot act
// on is how a check earns the right to be ignored.
func checkTrackedSecrets(ctx context.Context) service.Check {
	dir, err := os.Getwd()
	if err != nil {
		return service.Check{Name: "secrets", Status: "ok",
			Detail: fmt.Sprintf("skipped: %v", err)}
	}

	res, err := secrets.ScanTracked(ctx, dir)
	switch {
	case errors.Is(err, secrets.ErrNoRepository):
		return service.Check{Name: "secrets", Status: "ok",
			Detail: "not inside a git repository, nothing tracked to scan"}
	case errors.Is(err, secrets.ErrGitUnavailable):
		return service.Check{Name: "secrets", Status: "ok",
			Detail: "git is not installed, skipped the scan for committed tokens"}
	case err != nil:
		return service.Check{Name: "secrets", Status: "ok",
			Detail: fmt.Sprintf("skipped: %v", err)}
	}

	if len(res.Findings) == 0 {
		return service.Check{Name: "secrets", Status: "ok",
			Detail: fmt.Sprintf("%d tracked files scanned, no token-looking strings", res.Scanned)}
	}

	// Locations only. Echoing the match would put the secret into terminal
	// scrollback and CI logs, which is the failure this check exists to catch.
	var b strings.Builder
	fmt.Fprintf(&b, "%d line(s) in tracked files look like an integration token:", len(res.Findings))
	for _, f := range res.Findings {
		fmt.Fprintf(&b, "\n  %s:%d", f.Path, f.Line)
	}
	b.WriteString("\n  fix: rotate the token at https://www.notion.so/my-integrations, " +
		"remove it from the file, and keep it in " + config.TokenEnv + " or credentials.yml instead")
	return service.Check{Name: "secrets", Status: "warn", Detail: b.String()}
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
