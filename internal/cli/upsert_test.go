package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/config"
)

func TestUpsertCreatesAndIsQuietOnSuccess(t *testing.T) {
	var created bool
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[],"has_more":false}`))
		case "/v1/pages":
			created = true
			w.Write([]byte(cliRowJSON))
		default:
			w.Write([]byte(cliRowJSON))
		}
	})

	out := captureStdout(t, func() {
		code := executeArgs([]string{"upsert", "--ticket", "BDF-231", "--status", "Fatto", "--config", cfg})
		if code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if !created {
		t.Fatal("no page was created")
	}
	// Quiet on success: CI logs stay readable, --json is the opt-in.
	if out != "" {
		t.Fatalf("expected no output, got %q", out)
	}
}

// cmd.MarkFlagRequired("ticket") only checks that --ticket was passed, not
// that it carries a value, so `--ticket ""` used to exit 0 and create a row
// whose payload never carries the ticket property at all — a ghost row that
// no future get/set/upsert can ever reach again. A CI script with an
// unset $TICKET is exactly how this happens in practice.
func TestUpsertRejectsAnEmptyTicketBeforeCallingTheAPI(t *testing.T) {
	var wrote bool
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/pages" {
			wrote = true
		}
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})

	if code := executeArgs([]string{"upsert", "--ticket", "", "--title", "Ghost", "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
	if wrote {
		t.Fatal("an empty ticket reached the API; a ghost row was created")
	}
}

func TestUpsertRejectsAnUnknownStatusBeforeCallingTheAPI(t *testing.T) {
	var wrote bool
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/pages" {
			wrote = true
		}
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})

	// A rejected value is invalid usage, not a generic failure.
	if code := executeArgs([]string{"upsert", "--ticket", "X", "--status", "Fattto", "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if wrote {
		t.Fatal("a bogus status reached the API; a select property would have created it")
	}
}

func TestUpsertExitsDuplicateOnSeveralMatches(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `,` + cliRowJSON + `],"has_more":false}`))
	})

	if code := executeArgs([]string{"upsert", "--ticket", "BDF-231", "--config", cfg}); code != ExitDuplicate {
		t.Fatalf("exit code = %d, want %d", code, ExitDuplicate)
	}
}

// The exit-code table documents code 4 for upsert, set and get alike; set
// had no test pinning it, only upsert and get did.
func TestSetExitsDuplicateOnSeveralMatches(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `,` + cliRowJSON + `],"has_more":false}`))
	})

	if code := executeArgs([]string{"set", "--ticket", "BDF-231", "--status", "Fatto", "--config", cfg}); code != ExitDuplicate {
		t.Fatalf("exit code = %d, want %d", code, ExitDuplicate)
	}
}

func TestSetExitsNotFoundInsteadOfCreating(t *testing.T) {
	var created bool
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case "/v1/pages":
			created = true
			w.Write([]byte(cliRowJSON))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	})

	if code := executeArgs([]string{"set", "--ticket", "NOPE", "--status", "Fatto", "--config", cfg}); code != ExitNotFound {
		t.Fatalf("exit code = %d, want %d", code, ExitNotFound)
	}
	if created {
		t.Fatal("set created a row; only upsert may do that")
	}
}

// stubForAssignee answers schema, query and write, keeping the properties
// payload of the write so a test can assert on what reached Notion.
//
// It clears no environment of its own: withStubbedAPIProfile already isolates
// the user config dir and NOTION_TRACK_ME for every test, which is what these
// tests need — buildService resolves the identity through
// config.ResolveIdentity on every command, so a developer who exported
// NOTION_TRACK_ME, or simply has a credentials.yml, would otherwise have their
// own identity leak in here — and the one test that asserts "me" is *not*
// configured would silently pass for the wrong reason.
func stubForAssignee(t *testing.T, profile string, written *map[string]any) string {
	t.Helper()
	return withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
		default:
			var body struct {
				Properties map[string]any `json:"properties"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			*written = body.Properties
			w.Write([]byte(cliRowJSON))
		}
	}, profile)
}

func TestSetWritesTheResolvedAssignee(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--assignee", "mirko", "--config", cfg,
	}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	got, _ := json.Marshal(written["Referente"])
	if want := `{"select":{"name":"Mirko Spinato"}}`; string(got) != want {
		t.Errorf("Referente = %s, want %s", got, want)
	}
}

func TestSetUnassignClearsTheColumn(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--unassign", "--config", cfg,
	}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	got, _ := json.Marshal(written["Referente"])
	if want := `{"select":null}`; string(got) != want {
		t.Errorf("Referente = %s, want %s", got, want)
	}
}

func TestSetMeUsesTheConfiguredIdentity(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--assignee", "me", "--config", cfg,
	}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	got, _ := json.Marshal(written["Referente"])
	if want := `{"select":{"name":"Marco Arnulfo"}}`; string(got) != want {
		t.Errorf("Referente = %s, want %s", got, want)
	}
}

// TestAssigneeMeUsesTheIdentityFromCredentials proves the identity comes from
// credentials.yml, and that it is looked up under the *resolved* profile
// name rather than the raw --profile flag: the fixture's config.yml names
// "work" as default_profile, the identity is saved under "work", and the
// command below never passes --profile at all. If buildService looked up
// identities[""] (the flag's zero value) instead of identities[name], this
// would find nothing and fail with ErrNoIdentity.
func TestAssigneeMeUsesTheIdentityFromCredentials(t *testing.T) {
	var written map[string]any
	// assigneeProfileNoIdentity carries no legacy me:, so the only possible
	// source for a resolved value here is credentials.yml.
	cfg := stubForAssignee(t, assigneeProfileNoIdentity, &written)

	if err := config.SaveIdentity("work", "Andrea Ghidara"); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--assignee", "me", "--config", cfg,
	}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	got, _ := json.Marshal(written["Referente"])
	if want := `{"select":{"name":"Andrea Ghidara"}}`; string(got) != want {
		t.Errorf("Referente = %s, want %s", got, want)
	}
}

func TestAssigneeUsageErrorsAllExitTwo(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		args    []string
	}{
		{"both set and clear", assigneeProfile, []string{"--assignee", "mirko", "--unassign"}},
		{"an empty value", assigneeProfile, []string{"--assignee", ""}},
		{"an unknown name", assigneeProfile, []string{"--assignee", "Marko"}},
		{"an ambiguous name", assigneeProfile, []string{"--assignee", "ar"}},
		{"me with no identity", assigneeProfileNoIdentity, []string{"--assignee", "me"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var written map[string]any
			cfg := stubForAssignee(t, tt.profile, &written)

			args := append([]string{"set", "--ticket", "BDF-231"}, tt.args...)
			args = append(args, "--config", cfg)
			if code := executeArgs(args); code != ExitUsage {
				t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
			}
			if written != nil {
				t.Errorf("a usage error still wrote %v", written)
			}
		})
	}
}

func TestAssigneeOnAnUnmappedRoleExitsOne(t *testing.T) {
	// Not ExitUsage: the "role not mapped" message is the same untyped one the
	// other four roles have always produced, and typing it for assignee alone
	// would either change --due's exit code too or treat one role differently
	// from the rest for the identical condition. Both are worse than a 1.
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--assignee", "mirko", "--config", cfg,
	}); code != ExitError {
		t.Fatalf("exit code = %d, want %d (ExitError)", code, ExitError)
	}
}

func TestUnassignDryRunSaysWhatItWouldClear(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{
			"set", "--ticket", "BDF-231", "--unassign", "--dry-run", "--config", cfg,
		}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	if !strings.Contains(out, "Referente") {
		t.Errorf("output = %q, want it to name the column it would clear", out)
	}
	if written != nil {
		t.Errorf("a dry run wrote %v", written)
	}
}

func TestSetWritesTheResolvedPriority(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--priority", "alta", "--config", cfg,
	}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	got, _ := json.Marshal(written["Urgenza"])
	if want := `{"select":{"name":"ALTA"}}`; string(got) != want {
		t.Errorf("Urgenza = %s, want %s", got, want)
	}
}

func TestPriorityUsageErrorsExitTwo(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--priority", "URGENTISSIMA", "--config", cfg,
	}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
	if written != nil {
		t.Errorf("a usage error still wrote %v", written)
	}
}

// `--priority ""` used to be a silent no-op: unlike `--assignee ""` it was not
// rejected by bindShared's PreRunE, so it reached BuildProperties, was read as
// "leave this alone", and the write went through anyway. This is the
// priority's equivalent of TestAssigneeUsageErrorsAllExitTwo's "an empty
// value" case.
func TestPriorityEmptyValueExitsTwo(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--priority", "", "--config", cfg,
	}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
	if written != nil {
		t.Errorf("a usage error still wrote %v", written)
	}
}

func TestPriorityOnAnUnmappedRoleExitsOne(t *testing.T) {
	// Exit 1, not 2, exactly like every other unmapped role: the message comes
	// from an untyped fmt.Errorf shared with ticket/status/title/due, and
	// typing it for priority alone would change --due's exit code too. Asserted
	// so it stays a choice rather than turning into a surprise.
	// withStubbedAPI's default profile maps no priority — the same fixture the
	// assignee's twin test uses for this case.
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--priority", "ALTA", "--config", cfg,
	}); code != ExitError {
		t.Fatalf("exit code = %d, want %d (ExitError)", code, ExitError)
	}
}

func TestPriorityAndAssigneeTogether(t *testing.T) {
	// Two roles, one write: both columns must reach the payload.
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--priority", "media", "--assignee", "mirko", "--config", cfg,
	}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	urgenza, _ := json.Marshal(written["Urgenza"])
	referente, _ := json.Marshal(written["Referente"])
	if string(urgenza) != `{"select":{"name":"MEDIA"}}` {
		t.Errorf("Urgenza = %s", urgenza)
	}
	if string(referente) != `{"select":{"name":"Mirko Spinato"}}` {
		t.Errorf("Referente = %s", referente)
	}
}

// On the assignee, this exact gap — a dry run tested only at planFor's level,
// never through the CLI — was the plan review's only BLOCKER-class finding.
// This is the priority's equivalent: it proves --dry-run's printed output
// carries the RESOLVED value ("ALTA"), not the raw text the user typed
// ("alta"), and that nothing reaches the API.
func TestSetPriorityDryRunNamesTheResolvedValue(t *testing.T) {
	cfg := withStubbedAPIProfile(t, dryRunAPI(t, cliRowJSON), assigneeProfile)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{
			"set", "--ticket", "BDF-231", "--priority", "alta", "--dry-run", "--config", cfg,
		}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	if !strings.Contains(out, "Urgenza") {
		t.Errorf("output = %q, want it to name the column", out)
	}
	if !strings.Contains(out, "ALTA") {
		t.Errorf("output = %q, want the resolved value", out)
	}
	if strings.Contains(out, "alta") {
		t.Errorf("output = %q, printed the raw input instead of the resolved value", out)
	}
}

func TestSetByBoardIDUpdatesTheRow(t *testing.T) {
	// Recording only the path, and not the method, would still pass even if
	// the PATCH vanished entirely: SetByID's resolvePage issues a GET on this
	// same /v1/pages/{id} path before the PATCH, so the path alone proves
	// nothing about the write. Recording "METHOD path" and asserting on the
	// PATCH specifically closes that gap.
	var seen []string
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaWithIDJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + cliRowWithIDJSON + `],"has_more":false}`))
		default: // GET (resolvePage) then PATCH /v1/pages/{id}
			w.Write([]byte(cliRowWithIDJSON))
		}
	}, idProfileYAML)

	if code := executeArgs([]string{
		"set", "--id", "BDF-271", "--status", "Fatto", "--config", cfg,
	}); code != ExitOK {
		t.Fatalf("exit code = %d, want %d (ExitOK)", code, ExitOK)
	}
	// The write must land on the page the id resolved to.
	want := "PATCH /v1/pages/23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5"
	if !containsRequest(seen, want) {
		t.Errorf("requests = %v, want it to include %q", seen, want)
	}
}

func TestSetRejectsTwoWaysOfAddressingAtOnce(t *testing.T) {
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaWithIDJSON))
	}, idProfileYAML)

	if code := executeArgs([]string{
		"set", "--id", "BDF-271", "--page-id", "abc", "--status", "Fatto", "--config", cfg,
	}); code != ExitUsage {
		t.Errorf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}

// set.go's switch on Changed("id") is its own, independent of get.go's — this
// is set's twin of TestGetRejectsAnEmptyBoardID in get_test.go, guarding the
// same regression (branching on the value instead of on
// cmd.Flags().Changed("id")) on the command that actually writes. Without
// this test, set.go could regress that branch with nothing to catch it: the
// regression falls through to a ticket lookup, raises ErrEmptyTicket, and
// still exits 2 — the exit code alone cannot tell the two apart, only the
// message can.
func TestSetRejectsAnEmptyBoardID(t *testing.T) {
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaWithIDJSON))
	}, idProfileYAML)

	var code int
	errOut := captureStderr(t, func() {
		code = executeArgs([]string{"set", "--id", "", "--status", "Fatto", "--config", cfg})
	})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
	if !strings.Contains(errOut, "id must not be empty") {
		t.Errorf("stderr = %q, want it to report an empty id", errOut)
	}
}

func TestUpsertHasNoBoardIDFlag(t *testing.T) {
	// upsert's key is the ticket, and a row being created has no board id yet:
	// Notion assigns it. Offering --id there would be offering to address
	// something that does not exist.
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaWithIDJSON))
	}, idProfileYAML)

	if code := executeArgs([]string{
		"upsert", "--id", "BDF-271", "--config", cfg,
	}); code != ExitUsage {
		t.Errorf("exit code = %d, want %d for an unknown flag", code, ExitUsage)
	}
}
