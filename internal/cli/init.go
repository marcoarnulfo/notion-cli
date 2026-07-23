package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Interactive-prompt seams, all three replaced together in tests. term.
// ReadPassword needs a real terminal fd, which a test has no cheap way to
// fake, so tests swap these for plain functions instead of building one.
var (
	isInteractive = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	readToken     = func() (string, error) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	readLine = func() (string, error) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
)

// resolveInitToken finds the token the way every other command does
// (NOTION_TOKEN, then credentials.yml) and, only at an interactive terminal
// and only when neither source has it, offers to collect one and save it
// for next time. A non-interactive run — CI, a pipe, an agent — must never
// block on a prompt nobody can answer, so it falls straight back to the
// same ExitAuth error every other command already gives for a missing
// token.
func resolveInitToken(cmd *cobra.Command) (string, error) {
	token, _, err := config.LoadToken()
	if err != nil {
		return "", err
	}
	if token != "" {
		return token, nil
	}
	if !isInteractive() {
		return "", Errorf(ExitAuth, "no integration token found; set %s", config.TokenEnv)
	}
	return promptForToken(cmd)
}

// promptForToken asks for a token with no local echo, then asks whether to
// persist it. Saving is the recommended default (bare Enter accepts it)
// because the whole point of asking is to spare the user from re-exporting
// NOTION_TOKEN every session.
func promptForToken(cmd *cobra.Command) (string, error) {
	cmd.Println("No Notion integration token found.")
	cmd.Println("Create one at https://www.notion.so/my-integrations")
	cmd.Print("Token: ")
	// readTokenInterruptible, not readToken directly: a bare Ctrl-C here
	// terminates the process before term.ReadPassword's defer can restore
	// local echo, leaving the terminal broken until the user runs
	// `stty sane`. See internal/cli/interrupt.go.
	token, err := readTokenInterruptible()
	// term.ReadPassword echoes nothing, not even the Enter that ended input,
	// so the cursor is still sitting on the prompt line without this.
	cmd.Println()
	if err != nil {
		return "", Errorf(ExitError, "reading token: %v", err)
	}
	if token == "" {
		return "", Errorf(ExitAuth, "no integration token found; set %s", config.TokenEnv)
	}

	cmd.Print("Save it for future sessions? [Y/n] ")
	answer, err := readLine()
	if err != nil {
		return "", Errorf(ExitError, "reading answer: %v", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))

	// A prompt that persists a secret must be permissive about what counts as
	// "no": "nope", "N.", a typo, anything starting with n declines. Only
	// exact "y"/"yes"/empty should ever save; failing open here means a typo
	// writes a token to disk when the user meant to say no.
	if strings.HasPrefix(answer, "n") {
		// Never echo the token itself here (see the package-wide rule that it
		// must not appear in output): the user just typed it and still has it
		// wherever they copied it from, so a placeholder is enough to name the
		// exact command to run.
		cmd.Println("Not saved. For this session only, run:")
		cmd.Printf("  export %s=<paste your token>\n", config.TokenEnv)
		cmd.Println("  (notion-track can't set it for you: a child process can't modify its parent shell's environment)")
		return token, nil
	}

	if err := config.SaveToken(token); err != nil {
		return "", err
	}
	credPath, err := config.CredentialsPath()
	if err != nil {
		return "", err
	}
	// The permissions claim must describe the file that actually landed on
	// disk, not a constant that quietly goes stale (or lies) the moment
	// SaveToken's guarantee ever regresses. A Stat here catches that the
	// moment it happens instead of printing a false "0600" over a wide-open
	// secret.
	perm := "unknown"
	if info, statErr := os.Stat(credPath); statErr == nil {
		perm = fmt.Sprintf("%04o", info.Mode().Perm())
	}
	cmd.Printf("Saved to %s (permissions %s). Do not commit this file.\n", credPath, perm)
	return token, nil
}

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
			token, err := resolveInitToken(cmd)
			if err != nil {
				return err
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
//
// ticket, status and title are required: internal/service/doctor.go reports
// each of them as a "fail" when unmapped, and get/list/upsert key every
// lookup off them, so writing a profile with one left blank produces a
// config that is broken on first use. due is the only role doctor treats as
// optional, so it is the only one that may be left unmapped here too.
func validateMapping(schema *notion.Schema, ticket, status, title, due string) (string, error) {
	check := func(role, flag, name string, required bool, want ...string) (string, error) {
		if name == "" {
			if required {
				return "", fmt.Errorf("--%s is required: map the %s property before init writes a profile",
					flag, role)
			}
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

	if _, err := check("ticket", "ticket-prop", ticket, true, "rich_text", "title"); err != nil {
		return "", err
	}
	if _, err := check("title", "title-prop", title, true, "title"); err != nil {
		return "", err
	}
	if _, err := check("due", "due-prop", due, false, "date"); err != nil {
		return "", err
	}
	statusType, err := check("status", "status-prop", status, true, "status", "select")
	if err != nil {
		return "", err
	}
	return statusType, nil
}
