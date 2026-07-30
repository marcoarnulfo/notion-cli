package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
	"github.com/marcoarnulfo/notion-cli/internal/tui"
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

	// Saving is the recommended default (the prompt's own [Y/n] and the doc
	// comment above say so), so refusal is what has to be recognized
	// broadly: "nope", "N.", anything starting with n declines, and
	// everything else — "y", "yes", a bare Enter, but also "q" or a typo —
	// saves. That is deliberate, not an oversight: this prompt only ever
	// runs at an interactive terminal right after the user pasted the token
	// in, so the worst case of failing open is writing to a file the user
	// already trusted enough to type a secret into: 0600, this user's, and
	// print the location. Failing closed instead would mean a typo silently
	// discards a token the user meant to keep, sending them back through
	// the whole prompt next session for no reason.
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

// configFlags are the flags that say *what* to configure. Passing any of them
// is what tells init the caller already knows their answers, so the wizard
// stays out of the way. --profile and --config are deliberately absent: they
// say where to write the profile, not what goes in it, and the wizard honours
// them.
var configFlags = []string{
	"data-source-id", "database-id", "ticket-prop", "status-prop",
	"title-prop", "due-prop", "assignee-prop", "priority-prop", "id-prop", "me", "list",
}

func anyConfigFlagSet(cmd *cobra.Command) bool {
	for _, name := range configFlags {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

// identityOnly reports an `init --me <name>` with nothing else to configure.
// --me is a config flag like the rest — it keeps the wizard out of the way —
// but on its own it says something different from all the others: not "here is
// the data source", just "here is who I am".
func identityOnly(cmd *cobra.Command) bool {
	if !cmd.Flags().Changed("me") {
		return false
	}
	for _, name := range configFlags {
		if name != "me" && cmd.Flags().Changed(name) {
			return false
		}
	}
	return true
}

// runWizard is the seam that keeps bubbletea's runtime out of the tests. A
// test has no terminal to give it, and tea.NewProgram would block waiting for
// input nobody is there to type. The Model itself is tested directly, in
// internal/tui.
var runWizard = func(m tui.Model) (tui.Result, error) {
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return tui.Result{}, err
	}
	model, ok := final.(tui.Model)
	if !ok {
		return tui.Result{}, fmt.Errorf("wizard returned an unexpected model %T", final)
	}
	return model.Result(), nil
}

// runInitWizard is `notion-track init` with nothing else on the command line
// at an interactive terminal: pick a data source, confirm the mapping, save.
//
// The token is collected before and outside the TUI. promptForToken already
// reads without echo and restores the terminal on Ctrl-C (see interrupt.go);
// rebuilding that inside a bubbletea text input would mean reimplementing the
// one part where getting it wrong leaves the user's terminal broken. Anyone
// who already has a token never sees the step at all.
func runInitWizard(cmd *cobra.Command) error {
	token, err := resolveInitToken(cmd)
	if err != nil {
		return err
	}
	client := newClient(token)

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

	res, err := runWizard(tui.NewWizard(refs, func(id string) (*notion.Schema, error) {
		return client.GetSchema(cmd.Context(), id)
	}))
	if err != nil {
		return err
	}
	if res.Err != nil {
		return res.Err
	}
	if res.Cancelled {
		// Exit non-zero, so a script can tell "configured" from "the user
		// changed their mind". The wizard cannot write a partial profile, so
		// there is nothing to clean up.
		return Errorf(ExitError, "init cancelled, nothing was written")
	}

	// Belt and braces: the wizard only ever offers columns of a usable type,
	// so this cannot fail as things stand. It runs anyway because it is the
	// one thing standing between a future wizard bug and a profile that is
	// broken on first use — and it is where status_type comes from.
	statusType, err := validateMapping(res.Schema, res.Props)
	if err != nil {
		return Errorf(ExitUsage, "%v", err)
	}

	if err := saveInitProfile(cmd, config.Profile{
		DatabaseID:   res.Ref.DatabaseID,
		DataSourceID: res.Ref.ID,
		StatusType:   statusType,
		Properties:   res.Props,
		// The identity the wizard collected goes to credentials.yml via
		// saveInitIdentity below, never here: config.yml is shared, and Me
		// on the profile is the legacy field that lives there for
		// configurations written before the identity moved.
		Me: "",
	}, res.Schema.Title); err != nil {
		return err
	}
	return saveInitIdentity(cmd, res.Identity)
}

// saveInitIdentity writes the identity both profile-writing paths through init
// collect, so the wizard and the flags cannot drift into disagreeing about
// which profile it belongs to.
//
// It goes in credentials.yml, not the profile saveInitProfile just wrote:
// config.yml is committed and shared, and an identity written there would be
// everyone's. Called after the profile so that a failure to write it leaves
// a usable configuration behind rather than an identity pointing at a
// profile that does not exist.
//
// The profile name follows the same rule saveInitProfile uses — --profile,
// else the literal "default" — not cfg.DefaultProfile: an identity is
// per-profile, and resolving through the default here would save it under
// the wrong key the moment --profile is set without --config pointing at a
// fresh file. runInitIdentity resolves the name instead, and the difference
// is deliberate: see the comment there.
func saveInitIdentity(cmd *cobra.Command, identity string) error {
	if identity == "" {
		return nil
	}
	name, _ := cmd.Flags().GetString("profile")
	if name == "" {
		name = "default"
	}
	return saveIdentityFor(cmd, name, identity)
}

// saveIdentityFor persists one identity under one profile name and says where
// it landed. Both init paths end here so the file that gets named, and the
// shape of the confirmation, cannot drift apart.
//
// The profile is named in the message because the two callers key the identity
// differently and a user has no other way to see which key was used.
func saveIdentityFor(cmd *cobra.Command, profileName, identity string) error {
	if err := config.SaveIdentity(profileName, identity); err != nil {
		return err
	}
	credPath, err := config.CredentialsPath()
	if err != nil {
		return err
	}
	cmd.Printf("identity %q saved to %s for profile %q\n", identity, credPath, profileName)
	return nil
}

// runInitIdentity is `notion-track init --me <name>` on its own: it records who
// the user is and configures nothing else.
//
// That command is what every remediation message in the tool names — doctor's
// legacy-identity warning, service.ErrNoIdentity, the agent skill — and none of
// them is asking the user to re-describe a data source they configured long
// ago. So it reads the profile in use instead of writing one: the assignee
// column to validate the name against is already mapped there, and config.yml
// must not be touched at all (a file meant to be committed, edited as a side
// effect, turns up unexplained in someone's git status).
func runInitIdentity(cmd *cobra.Command, me string) error {
	path, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfigForFlag(path)
	if err != nil {
		return err
	}

	requested, _ := cmd.Flags().GetString("profile")
	// The RESOLVED name — --profile, then NOTION_TRACK_PROFILE, then
	// default_profile — because that is the profile every other command will
	// read the identity back under (see buildService).
	//
	// This is where init's two profile-key rules meet, and they differ on
	// purpose: an init that CREATES a profile saves the identity for the
	// profile it just wrote (--profile, else "default" — saveInitIdentity),
	// because resolving through a default_profile that names some other
	// profile would file it under a profile the user was not configuring. An
	// init that only SETS an identity has created nothing, so the profile it
	// belongs to is simply the one in use.
	name := cfg.ProfileName(requested)
	if name == "" {
		return Errorf(ExitUsage,
			"--me on its own sets the identity of a profile that already exists, and none is configured\n"+
				"  fix: run 'notion-track init' to configure one first")
	}
	profile, err := cfg.Resolve(name)
	if err != nil {
		return Errorf(ExitUsage, "%v", err)
	}
	column := profile.Properties.Assignee
	if column == "" {
		return Errorf(ExitUsage,
			"profile %q maps no assignee column, so there is nothing to resolve %q against\n"+
				"  fix: rerun init with --assignee-prop <name> to map one", name, me)
	}

	token, err := resolveInitToken(cmd)
	if err != nil {
		return err
	}
	// Validated against the live schema for the same reason the flag path
	// does it: a typo has to fail here, not on the first --assignee me.
	schema, err := newClient(token).GetSchema(cmd.Context(), profile.DataSourceID)
	if err != nil {
		return err
	}
	prop, ok := schema.Properties[column]
	if !ok {
		return Errorf(ExitUsage,
			"the profile's assignee column %q no longer exists in this data source\n"+
				"  fix: run 'notion-track doctor' to see what the mapping should be", column)
	}
	resolved, err := tracker.ResolveOption("me", me, prop.Options)
	if err != nil {
		return Errorf(ExitUsage, "%v", err)
	}
	return saveIdentityFor(cmd, name, resolved)
}

// saveInitProfile writes the profile both paths through init produce, so the
// wizard and the flags cannot drift into writing subtly different configs.
func saveInitProfile(cmd *cobra.Command, profile config.Profile, sourceTitle string) error {
	path, _ := cmd.Flags().GetString("config")
	cfg, err := loadExistingOrNew(path)
	if err != nil {
		return err
	}

	name, _ := cmd.Flags().GetString("profile")
	if name == "" {
		name = "default"
	}
	cfg.Profiles[name] = profile
	if cfg.DefaultProfile == "" {
		cfg.DefaultProfile = name
	}

	if err := saveConfigTo(cfg, path); err != nil {
		return err
	}
	cmd.Printf("profile %q configured for data source %q\n", name, sourceTitle)
	return nil
}

// newInitCmd writes a profile, from flags or from the interactive wizard. The
// flag form is what CI and agents use, and it is untouched by the wizard.
func newInitCmd() *cobra.Command {
	var (
		databaseID   string
		dataSourceID string
		ticketProp   string
		statusProp   string
		titleProp    string
		dueProp      string
		assigneeProp string
		priorityProp string
		idProp       string
		me           string
		list         bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Configure a profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A bare `init` at a terminal is the wizard. Anywhere else — CI,
			// a pipe, an agent — it stays the usage error below: a TUI nobody
			// can answer would hang the run.
			if !anyConfigFlagSet(cmd) && isInteractive() {
				return runInitWizard(cmd)
			}

			// Before the --data-source-id guard below, which exists to stop a
			// profile being written without one: `init --me <name>` writes no
			// profile at all, and demanding a data source id to record an
			// identity would make every "run 'notion-track init --me <name>'"
			// message in this tool unrunnable as printed.
			if identityOnly(cmd) {
				return runInitIdentity(cmd, me)
			}

			// Usage must be validated before anything that could prompt for
			// and persist a secret: an interactive user running init without
			// --data-source-id used to be asked for the token, and could
			// save it, before ever hearing the invocation was unusable.
			// --data-source-id has nothing to do with the token, so nothing
			// here should depend on one to reach this check.
			if !list && dataSourceID == "" {
				return Errorf(ExitUsage,
					"--data-source-id is required\n"+
						"  run 'notion-track init --list' to see the data sources shared with your integration")
			}

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

			// Validate the mapping against the live schema before writing a
			// config that would fail on first use.
			schema, err := client.GetSchema(cmd.Context(), dataSourceID)
			if err != nil {
				return err
			}
			props := config.Properties{
				Ticket: ticketProp, Status: statusProp, Title: titleProp, Due: dueProp,
				Assignee: assigneeProp, Priority: priorityProp, ID: idProp,
			}
			statusType, err := validateMapping(schema, props)
			if err != nil {
				return Errorf(ExitUsage, "%v", err)
			}

			resolvedMe := ""
			if me != "" {
				if assigneeProp == "" {
					return Errorf(ExitUsage,
						"--me needs an assignee column to resolve against\n"+
							"  fix: pass --assignee-prop <name> as well")
				}
				resolvedMe, err = tracker.ResolveOption("me", me, schema.Properties[assigneeProp].Options)
				if err != nil {
					return Errorf(ExitUsage, "%v", err)
				}
			}

			if err := saveInitProfile(cmd, config.Profile{
				DatabaseID:   databaseID,
				DataSourceID: dataSourceID,
				StatusType:   statusType,
				Properties:   props,
			}, schema.Title); err != nil {
				return err
			}

			return saveInitIdentity(cmd, resolvedMe)
		},
	}

	cmd.Flags().StringVar(&databaseID, "database-id", "", "database id")
	cmd.Flags().StringVar(&dataSourceID, "data-source-id", "", "data source id (required)")
	cmd.Flags().StringVar(&ticketProp, "ticket-prop", "", "property holding the ticket key")
	cmd.Flags().StringVar(&statusProp, "status-prop", "", "property holding the status")
	cmd.Flags().StringVar(&titleProp, "title-prop", "", "title property")
	cmd.Flags().StringVar(&dueProp, "due-prop", "", "date property (optional)")
	cmd.Flags().StringVar(&assigneeProp, "assignee-prop", "", "select property holding the assignee (optional)")
	cmd.Flags().StringVar(&priorityProp, "priority-prop", "", "select property holding the priority (optional)")
	cmd.Flags().StringVar(&idProp, "id-prop", "", "unique_id property holding the board id, e.g. TASK-271 (optional)")
	cmd.Flags().StringVar(&me, "me", "", "the assignee value '--assignee me' stands for; on its own, sets only the identity, for the profile in use (optional)")
	cmd.Flags().BoolVar(&list, "list", false, "list the data sources shared with the integration and exit")
	return cmd
}

// validateMapping checks each mapped property against the schema and returns
// the status property's actual type.
//
// It takes the whole Properties struct rather than one string per role: the
// roles are named at the call site by field name, so no caller can silently
// swap two of them, and adding a role is a line here instead of a new
// positional parameter at every call site.
//
// ticket, status and title are required: internal/service/doctor.go reports
// each of them as a "fail" when unmapped, and get/list/upsert key every
// lookup off them, so writing a profile with one left blank produces a
// config that is broken on first use. due, assignee, priority and id are the
// roles doctor treats as optional, so they are the only ones that may be
// left unmapped here too.
func validateMapping(schema *notion.Schema, p config.Properties) (string, error) {
	check := func(role, flag, name string, required bool, want ...string) (string, error) {
		if name == "" {
			if required {
				return "", fmt.Errorf("--%s is required: map the %s property before init writes a profile",
					flag, role)
			}
			return "", nil
		}
		prop, ok := schema.Properties[name]
		if !ok {
			return "", fmt.Errorf("%s property %q does not exist in this data source", role, name)
		}
		for _, t := range want {
			if prop.Type == t {
				return prop.Type, nil
			}
		}
		return "", fmt.Errorf("%s property %q has type %q, which is not usable as %s",
			role, name, prop.Type, role)
	}

	if _, err := check("ticket", "ticket-prop", p.Ticket, true, "rich_text", "title"); err != nil {
		return "", err
	}
	if _, err := check("title", "title-prop", p.Title, true, "title"); err != nil {
		return "", err
	}
	if _, err := check("due", "due-prop", p.Due, false, "date"); err != nil {
		return "", err
	}
	if _, err := check("assignee", "assignee-prop", p.Assignee, false, "select"); err != nil {
		return "", err
	}
	if _, err := check("priority", "priority-prop", p.Priority, false, "select"); err != nil {
		return "", err
	}
	if _, err := check("id", "id-prop", p.ID, false, "unique_id"); err != nil {
		return "", err
	}
	statusType, err := check("status", "status-prop", p.Status, true, "status", "select")
	if err != nil {
		return "", err
	}
	return statusType, nil
}
