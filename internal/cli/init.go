package cli

import (
	"fmt"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/spf13/cobra"
)

// newInitCmd writes a profile from flags. The interactive TUI wizard is added
// separately; this form is what CI and agents use.
func newInitCmd() *cobra.Command {
	var (
		databaseID   string
		dataSourceID string
		ticketProp   string
		statusProp   string
		titleProp    string
		dueProp      string
		list         bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Configure a profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, _ := config.Token()
			if token == "" {
				return Errorf(ExitAuth, "no integration token found; set %s", config.TokenEnv)
			}
			client := newClient(token)

			// --list is how a user discovers the id that --data-source-id wants.
			if list {
				refs, err := client.ListDataSources(cmd.Context())
				if err != nil {
					return err
				}
				if len(refs) == 0 {
					return Errorf(ExitError,
						"no data sources are shared with this integration\n"+
							"  fix: a workspace owner must open the database in Notion →\n"+
							"       ••• → Connections → add the integration, then retry")
				}
				for _, r := range refs {
					cmd.Printf("%s\t%s\n", r.ID, r.Title)
				}
				return nil
			}

			if dataSourceID == "" {
				return Errorf(ExitUsage,
					"--data-source-id is required\n"+
						"  run 'notion-track init --list' to see the data sources shared with your integration")
			}

			// Validate the mapping against the live schema before writing a
			// config that would fail on first use.
			schema, err := client.GetSchema(cmd.Context(), dataSourceID)
			if err != nil {
				return err
			}
			statusType, err := validateMapping(schema, ticketProp, statusProp, titleProp, dueProp)
			if err != nil {
				return Errorf(ExitUsage, "%v", err)
			}

			path, _ := cmd.Flags().GetString("config")
			cfg, err := loadExistingOrNew(path)
			if err != nil {
				return err
			}

			name, _ := cmd.Flags().GetString("profile")
			if name == "" {
				name = "default"
			}
			cfg.Profiles[name] = config.Profile{
				DatabaseID:   databaseID,
				DataSourceID: dataSourceID,
				StatusType:   statusType,
				Properties: config.Properties{
					Ticket: ticketProp, Status: statusProp, Title: titleProp, Due: dueProp,
				},
			}
			if cfg.DefaultProfile == "" {
				cfg.DefaultProfile = name
			}

			if err := saveConfigTo(cfg, path); err != nil {
				return err
			}
			cmd.Printf("profile %q configured for data source %q\n", name, schema.Title)
			return nil
		},
	}

	cmd.Flags().StringVar(&databaseID, "database-id", "", "database id")
	cmd.Flags().StringVar(&dataSourceID, "data-source-id", "", "data source id (required)")
	cmd.Flags().StringVar(&ticketProp, "ticket-prop", "", "property holding the ticket key")
	cmd.Flags().StringVar(&statusProp, "status-prop", "", "property holding the status")
	cmd.Flags().StringVar(&titleProp, "title-prop", "", "title property")
	cmd.Flags().StringVar(&dueProp, "due-prop", "", "date property (optional)")
	cmd.Flags().BoolVar(&list, "list", false, "list the data sources shared with the integration and exit")
	return cmd
}

// validateMapping checks each mapped property against the schema and returns
// the status property's actual type.
func validateMapping(schema *notion.Schema, ticket, status, title, due string) (string, error) {
	check := func(role, name string, want ...string) (string, error) {
		if name == "" {
			return "", nil
		}
		p, ok := schema.Properties[name]
		if !ok {
			return "", fmt.Errorf("%s property %q does not exist in this data source", role, name)
		}
		for _, t := range want {
			if p.Type == t {
				return p.Type, nil
			}
		}
		return "", fmt.Errorf("%s property %q has type %q, which is not usable as %s",
			role, name, p.Type, role)
	}

	if _, err := check("ticket", ticket, "rich_text", "title"); err != nil {
		return "", err
	}
	if _, err := check("title", title, "title"); err != nil {
		return "", err
	}
	if _, err := check("due", due, "date"); err != nil {
		return "", err
	}
	statusType, err := check("status", status, "status", "select")
	if err != nil {
		return "", err
	}
	return statusType, nil
}
