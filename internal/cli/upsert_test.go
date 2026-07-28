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
// NOTION_TRACK_ME is cleared deliberately: config.Resolve lets it override the
// profile, so a developer who followed the README and exported it would
// otherwise have their own identity leak into every test here — and the one
// test that asserts "me" is *not* configured would silently pass for the wrong
// reason.
func stubForAssignee(t *testing.T, profile string, written *map[string]any) string {
	t.Helper()
	t.Setenv(config.MeEnv, "")
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
	t.Setenv(config.MeEnv, "")
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

func TestPriorityOnAnUnmappedRoleExitsOne(t *testing.T) {
	// Exit 1, not 2, exactly like every other unmapped role: the message comes
	// from an untyped fmt.Errorf shared with ticket/status/title/due, and
	// typing it for priority alone would change --due's exit code too. Asserted
	// so it stays a choice rather than turning into a surprise.
	// withStubbedAPI's default profile maps no priority — the same fixture the
	// assignee's twin test uses for this case.
	t.Setenv(config.MeEnv, "")
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
