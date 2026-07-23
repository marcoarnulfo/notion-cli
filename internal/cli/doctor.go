package cli

import (
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

			for _, c := range checks {
				if c.Status == "fail" {
					return Errorf(ExitError, "one or more checks failed")
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	return cmd
}
