package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/tui"
)

// withInteractivePrompt swaps the three prompt seams and restores them on
// cleanup, so each test only states what the fake terminal returns.
func withInteractivePrompt(t *testing.T, interactive bool, token func() (string, error), line func() (string, error)) {
	t.Helper()
	oldInteractive, oldReadToken, oldReadLine := isInteractive, readToken, readLine
	isInteractive = func() bool { return interactive }
	if token != nil {
		readToken = token
	}
	if line != nil {
		readLine = line
	}
	t.Cleanup(func() {
		isInteractive, readToken, readLine = oldInteractive, oldReadToken, oldReadLine
	})
}

// The worst regression this feature could introduce: a CI job blocking
// forever on a prompt nobody can answer. isInteractive is forced false and
// both read seams panic the test if ever called, so any accidental prompt
// fails loudly instead of hanging.
func TestInitNonInteractiveNeverPrompts(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false,
		func() (string, error) { t.Fatal("readToken called in a non-interactive run"); return "", nil },
		func() (string, error) { t.Fatal("readLine called in a non-interactive run"); return "", nil },
	)

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1",
		"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
		"--config", cfg,
	})
	if code != ExitAuth {
		t.Fatalf("exit code = %d, want %d (ExitAuth)", code, ExitAuth)
	}
}

// An already-available token (env or file) must skip the prompt entirely,
// even at an interactive terminal: asking again would be pure friction.
func TestInitSkipsPromptWhenATokenIsAlreadyAvailable(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	// withStubbedAPI already exported NOTION_TOKEN; leave it set.
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, true,
		func() (string, error) {
			t.Fatal("readToken called though a token was already available")
			return "", nil
		},
		func() (string, error) {
			t.Fatal("readLine called though a token was already available")
			return "", nil
		},
	)

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1",
		"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
		"--config", cfg,
	})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (ExitOK)", code, ExitOK)
	}
}

// The default answer ("just press Enter") must save the token, and the
// token itself must never show up in anything printed along the way.
func TestInitInteractivePromptsAndSavesTokenByDefault(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, true,
		func() (string, error) { return "ntn_typed", nil },
		func() (string, error) { return "", nil }, // bare Enter accepts the recommended default
	)

	var code int
	out := captureStdout(t, func() {
		code = executeArgs([]string{
			"init", "--data-source-id", "ds1",
			"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
			"--config", cfg,
		})
	})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d: %s", code, ExitOK, out)
	}
	if strings.Contains(out, "ntn_typed") {
		t.Fatalf("the token leaked into output: %q", out)
	}

	tok, source, err := config.LoadToken()
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if tok != "ntn_typed" || source != "file" {
		t.Fatalf("LoadToken() = %q, %q, want ntn_typed, file", tok, source)
	}

	// The printed permission claim must describe the file that actually
	// landed on disk, not a hardcoded string that could go stale.
	credPath, err := config.CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(credPath)
	if err != nil {
		t.Fatal(err)
	}
	wantPerm := fmt.Sprintf("%04o", info.Mode().Perm())
	if !strings.Contains(out, "permissions "+wantPerm) {
		t.Fatalf("output %q does not report the actual on-disk permissions (%s)", out, wantPerm)
	}
}

// Declining must not write anything, and the fallback command it prints
// must not itself leak the secret it's trying to keep off disk.
func TestInitInteractiveDeclinesSave(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, true,
		func() (string, error) { return "ntn_typed", nil },
		func() (string, error) { return "n", nil },
	)

	var code int
	out := captureStdout(t, func() {
		code = executeArgs([]string{
			"init", "--data-source-id", "ds1",
			"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
			"--config", cfg,
		})
	})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d: %s", code, ExitOK, out)
	}
	if strings.Contains(out, "ntn_typed") {
		t.Fatalf("the token leaked into output: %q", out)
	}
	if !strings.Contains(out, "export "+config.TokenEnv+"=") {
		t.Fatalf("output does not show the export line to run for this session: %q", out)
	}

	credPath, err := config.CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credentials file was written despite declining to save (stat err = %v)", err)
	}
}

// A command bound to fail anyway must fail before it asks for a secret.
// Previously an interactive user running init without --data-source-id was
// prompted for the token — and could save it — before ever hearing that the
// invocation was unusable; the flag validation only ran afterwards. Missing
// --data-source-id has nothing to do with the token, so there is no reason
// the prompt should ever have been reachable on this path.
func TestInitValidatesFlagsBeforePromptingForAToken(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)
	// readTokenInterruptible calls readToken from its own goroutine, where
	// t.Fatal is unsafe to call directly (it must run on the test's own
	// goroutine) — record the call instead and assert on it afterwards.
	var readTokenCalled, readLineCalled atomic.Bool
	withInteractivePrompt(t, true,
		func() (string, error) { readTokenCalled.Store(true); return "", nil },
		func() (string, error) { readLineCalled.Store(true); return "", nil },
	)

	code := executeArgs([]string{
		"init",
		"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
		"--config", cfg,
	})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
	if readTokenCalled.Load() {
		t.Error("readToken was called before flags were validated")
	}
	if readLineCalled.Load() {
		t.Error("readLine was called before flags were validated")
	}

	credPath, err := config.CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credentials file was written despite the command being doomed to fail (stat err = %v)", err)
	}
}

// A prompt that persists a secret must be permissive about what counts as
// "no": only "n" and "no" used to decline, so a typo or a slightly longer
// answer like "nope" fell through to the save branch and silently wrote the
// token to disk — the opposite of what the user typed to avoid.
func TestInitInteractiveDeclineAcceptsAnyAnswerStartingWithN(t *testing.T) {
	for _, answer := range []string{"nope", "N", "N.", "No thanks", "never"} {
		t.Run(answer, func(t *testing.T) {
			cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(cliSchemaJSON))
			})
			t.Setenv(config.TokenEnv, "")
			withIsolatedUserConfigDir(t)
			withInteractivePrompt(t, true,
				func() (string, error) { return "ntn_typed", nil },
				func() (string, error) { return answer, nil },
			)

			code := executeArgs([]string{
				"init", "--data-source-id", "ds1",
				"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
				"--config", cfg,
			})
			if code != ExitOK {
				t.Fatalf("exit code = %d, want %d", code, ExitOK)
			}

			credPath, err := config.CredentialsPath()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(credPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("answer %q was not treated as a decline: credentials file was written (stat err = %v)", answer, err)
			}
		})
	}
}

// An empty answer to the token prompt means the user still has no usable
// token: this must fail exactly like every other "no token" path.
func TestInitInteractiveEmptyTokenExitsAuth(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, true,
		func() (string, error) { return "", nil },
		nil,
	)

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1",
		"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
		"--config", cfg,
	})
	if code != ExitAuth {
		t.Fatalf("exit code = %d, want %d (ExitAuth)", code, ExitAuth)
	}
}

// withFakeWizard swaps the bubbletea launch for a function that returns a
// canned outcome, so the wiring around the wizard can be tested without a
// terminal. The Model's own behaviour is covered in internal/tui.
func withFakeWizard(t *testing.T, res tui.Result, err error) *int {
	t.Helper()
	var calls int
	old := runWizard
	runWizard = func(tui.Model) (tui.Result, error) {
		calls++
		return res, err
	}
	t.Cleanup(func() { runWizard = old })
	return &calls
}

const wizardListJSON = `{"results":[{"id":"ds1","name":"Tasks",
	"parent":{"type":"database_id","database_id":"db1"}}],"has_more":false}`

// stubbedWizardAPI answers the two calls the wizard path makes before the TUI
// opens: the data source listing, and the schema of whichever one is picked.
func stubbedWizardAPI(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/data_sources/ds1":
		w.Write([]byte(cliSchemaJSON))
	default:
		w.Write([]byte(wizardListJSON))
	}
}

func TestInitWithNoFlagsAtATerminalRunsTheWizard(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedWizardAPI)
	withInteractivePrompt(t, true, nil, nil)
	calls := withFakeWizard(t, tui.Result{
		Ref: notion.DataSourceRef{ID: "ds1", Title: "Tasks", DatabaseID: "db1"},
		Schema: &notion.Schema{DataSourceID: "ds1", Title: "Tasks", Properties: map[string]notion.Property{
			"Name":   {Name: "Name", Type: "title"},
			"Ticket": {Name: "Ticket", Type: "rich_text"},
			"Stato":  {Name: "Stato", Type: "status"},
		}},
		Props: config.Properties{Ticket: "Ticket", Status: "Stato", Title: "Name"},
	}, nil)

	if code := executeArgs([]string{"init", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if *calls != 1 {
		t.Fatalf("wizard ran %d times, want once", *calls)
	}

	written, err := config.LoadFrom(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p := written.Profiles["default"]
	// The database id comes off the picked data source, which is why the
	// wizard never has to ask for it.
	if p.DatabaseID != "db1" || p.DataSourceID != "ds1" {
		t.Errorf("ids = %q/%q, want db1/ds1", p.DatabaseID, p.DataSourceID)
	}
	if p.Properties.Ticket != "Ticket" || p.Properties.Status != "Stato" || p.Properties.Title != "Name" {
		t.Errorf("properties = %+v", p.Properties)
	}
	// status_type is derived from the live schema by validateMapping, not
	// carried by the wizard.
	if p.StatusType != "status" {
		t.Errorf("status_type = %q, want status", p.StatusType)
	}
}

// --profile says where to write, not what to write, so it must not turn the
// wizard off.
func TestInitWithOnlyProfileFlagStillRunsTheWizard(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedWizardAPI)
	withInteractivePrompt(t, true, nil, nil)
	calls := withFakeWizard(t, tui.Result{
		Ref: notion.DataSourceRef{ID: "ds1", Title: "Tasks", DatabaseID: "db1"},
		Schema: &notion.Schema{Title: "Tasks", Properties: map[string]notion.Property{
			"Name":   {Name: "Name", Type: "title"},
			"Ticket": {Name: "Ticket", Type: "rich_text"},
			"Stato":  {Name: "Stato", Type: "status"},
		}},
		Props: config.Properties{Ticket: "Ticket", Status: "Stato", Title: "Name"},
	}, nil)

	if code := executeArgs([]string{"init", "--profile", "work", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if *calls != 1 {
		t.Fatalf("wizard ran %d times, want once", *calls)
	}

	written, err := config.LoadFrom(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := written.Profiles["work"]; !ok {
		t.Errorf("profiles = %v, want one named work", written.Profiles)
	}
}

// Walking away must leave no trace and must be distinguishable from success,
// or a script cannot tell "configured" from "changed my mind".
func TestCancellingTheWizardWritesNothingAndExitsNonZero(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedWizardAPI)
	withInteractivePrompt(t, true, nil, nil)
	withFakeWizard(t, tui.Result{Cancelled: true}, nil)

	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}

	if code := executeArgs([]string{"init", "--config", cfg}); code == ExitOK {
		t.Fatal("cancelling the wizard exited 0")
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Errorf("a config was written despite the cancellation: %v", err)
	}
}

// The flag path is what CI and agents use, and the wizard must stay out of it.
func TestInitWithConfigFlagsNeverOpensTheWizard(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedWizardAPI)
	withInteractivePrompt(t, true, nil, nil)
	calls := withFakeWizard(t, tui.Result{}, nil)

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1",
		"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
		"--config", cfg,
	})

	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if *calls != 0 {
		t.Fatal("the wizard ran on the flag path")
	}
}

// The guarantee CI depends on: no terminal means no TUI, just the same usage
// error as before the wizard existed.
func TestInitWithNoFlagsWithoutATerminalStillFailsOnUsage(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedWizardAPI)
	withInteractivePrompt(t, false, nil, nil)
	calls := withFakeWizard(t, tui.Result{}, nil)

	if code := executeArgs([]string{"init", "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (usage)", code, ExitUsage)
	}
	if *calls != 0 {
		t.Fatal("the wizard ran without a terminal")
	}
}

// An integration nobody shared a database with cannot be configured, and the
// wizard must say so instead of opening onto an empty list.
func TestTheWizardRefusesToOpenWithNoDataSources(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})
	withInteractivePrompt(t, true, nil, nil)
	calls := withFakeWizard(t, tui.Result{}, nil)

	if code := executeArgs([]string{"init", "--config", cfg}); code == ExitOK {
		t.Fatal("exit code 0 with no data sources to configure")
	}
	if *calls != 0 {
		t.Fatal("the wizard opened with nothing to pick")
	}
}

// initArgs is the flag form of init for the shared fixture board, with
// whatever the test adds on top.
//
// --profile work is not decoration: withStubbedAPI writes a config whose
// default_profile is already "work", and saveInitProfile writes to "default"
// when no profile is named, leaving default_profile untouched because it is not
// empty. Without this, the test reads back the OLD profile and fails against a
// perfectly correct implementation.
func initArgs(cfg string, extra ...string) []string {
	args := []string{
		"init", "--data-source-id", "ds1", "--profile", "work",
		"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
	}
	args = append(args, extra...)
	return append(args, "--config", cfg)
}

// writtenProfile reads back the profile init just wrote.
//
// The env is cleared first: Resolve applies NOTION_TRACK_ME over whatever the
// file says, so without this the assertion on Me would read the developer's
// shell instead of the file under test.
func writtenProfile(t *testing.T, path string) config.Profile {
	t.Helper()
	t.Setenv(config.MeEnv, "")
	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("reading back the config: %v", err)
	}
	p, err := cfg.Resolve("")
	if err != nil {
		t.Fatalf("resolving the written profile: %v", err)
	}
	return p
}

func TestInitMapsTheAssigneeColumn(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	if code := executeArgs(initArgs(cfg, "--assignee-prop", "Referente")); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if got := writtenProfile(t, cfg).Properties.Assignee; got != "Referente" {
		t.Errorf("assignee = %q, want %q", got, "Referente")
	}
}

func TestInitRejectsAnAssigneeColumnOfTheWrongType(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	// Name is the title column: usable as a title, never as an assignee.
	if code := executeArgs(initArgs(cfg, "--assignee-prop", "Name")); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}

func TestInitMapsThePriorityColumn(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	if code := executeArgs(initArgs(cfg, "--priority-prop", "Urgenza")); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if got := writtenProfile(t, cfg).Properties.Priority; got != "Urgenza" {
		t.Errorf("priority = %q, want %q", got, "Urgenza")
	}
}

func TestInitRejectsAPriorityColumnOfTheWrongType(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	// Name is the title column: never usable as a priority.
	if code := executeArgs(initArgs(cfg, "--priority-prop", "Name")); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}

func TestPriorityPropIsAConfigFlag(t *testing.T) {
	// Missing from configFlags, `init --priority-prop X` at a terminal opens
	// the wizard instead of configuring — silently, and only interactively.
	var found bool
	for _, f := range configFlags {
		if f == "priority-prop" {
			found = true
		}
	}
	if !found {
		t.Error("priority-prop is missing from configFlags")
	}
}

func TestAssigneeFlagsAreConfigFlags(t *testing.T) {
	// A config flag means "the caller knows their answers", which is what keeps
	// init out of the wizard. Forgetting to register one makes
	// `init --assignee-prop X` at a terminal open the TUI instead.
	for _, name := range []string{"assignee-prop", "me"} {
		var found bool
		for _, f := range configFlags {
			if f == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is missing from configFlags", name)
		}
	}
}

func TestInitMeStoresTheCanonicalValue(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	code := executeArgs(initArgs(cfg, "--assignee-prop", "Referente", "--me", "mirko"))
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if got := writtenProfile(t, cfg).Me; got != "Mirko Spinato" {
		t.Errorf("me = %q, want the canonical option %q", got, "Mirko Spinato")
	}
}

func TestInitMeNeedsTheAssigneeColumn(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	if code := executeArgs(initArgs(cfg, "--me", "mirko")); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}

func TestInitMeWarnsThatTheConfigIsShared(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	errOut := captureStderr(t, func() {
		executeArgs(initArgs(cfg, "--assignee-prop", "Referente", "--me", "mirko"))
	})
	if !strings.Contains(errOut, config.MeEnv) {
		t.Errorf("stderr = %q, want it to point at %s", errOut, config.MeEnv)
	}
}

// cliSchemaWithIDJSON is cliSchemaJSON plus the unique_id column the id role
// maps onto. A separate const, so the fixtures the existing tests were written
// against keep the property set they assert on.
const cliSchemaWithIDJSON = `{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{
	"Name":{"name":"Name","type":"title","title":{}},
	"Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
	"Stato":{"name":"Stato","type":"status","status":{"options":[{"name":"Fatto"}]}},
	"ID":{"name":"ID","type":"unique_id","unique_id":{"prefix":"BDF"}}}}`

func TestInitMapsTheIDColumn(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaWithIDJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	if code := executeArgs(initArgs(cfg, "--id-prop", "ID")); code != ExitOK {
		t.Fatalf("exit code = %d, want %d (ExitOK)", code, ExitOK)
	}
	if got := writtenProfile(t, cfg).Properties.ID; got != "ID" {
		t.Errorf("Properties.ID = %q, want %q", got, "ID")
	}
}

func TestInitRejectsAnIDColumnOfTheWrongType(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaWithIDJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	// A rich_text column cannot carry Notion's own row id.
	if code := executeArgs(initArgs(cfg, "--id-prop", "Ticket")); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}
