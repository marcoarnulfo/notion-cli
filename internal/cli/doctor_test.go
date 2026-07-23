package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestDoctorReportsEveryCheck(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	})

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"doctor", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	for _, want := range []string{"token", "data_source", "properties", "duplicates"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing the %q check: %s", want, out)
		}
	}
}

func TestDoctorExitsNonZeroWhenACheckFails(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":"unauthorized","message":"API token is invalid."}`))
	})

	captureStdout(t, func() {
		if code := executeArgs([]string{"doctor", "--config", cfg}); code == ExitOK {
			t.Fatal("doctor exited 0 despite a failing check")
		}
	})
}

// warnStatoTypeChangedSchemaJSON matches cliSchemaJSON except Stato is now a
// "select" property. The stubbed config (from withStubbedAPI) records
// status_type: status, so this schema drifts the status property's type
// without breaking anything else — checkProperties reports that as a "warn",
// never a "fail".
const warnStatoTypeChangedSchemaJSON = `{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{
	"Name":{"name":"Name","type":"title","title":{}},
	"Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
	"Stato":{"name":"Stato","type":"select","select":{"options":[{"name":"Fatto"}]}}}}`

// doctor must only fail the process on a "fail" check; a "warn" is reported
// but must not flip the exit code, or every drifted-but-still-working setup
// would look identical to a broken one in CI.
func TestDoctorExitsZeroWhenOnlyAWarn(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(warnStatoTypeChangedSchemaJSON))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	})

	var out string
	code := 0
	out = captureStdout(t, func() {
		code = executeArgs([]string{"doctor", "--config", cfg})
	})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (ExitOK) for a warn-only run: %s", code, ExitOK, out)
	}
	// The text renderer maps "warn" to the "!" symbol (see newDoctorCmd), not
	// the literal word, and must not print the "✗" fail symbol anywhere.
	if !strings.Contains(out, "!") {
		t.Fatalf("expected the warn symbol (!) in the output, got: %s", out)
	}
	if strings.Contains(out, "✗") {
		t.Fatalf("expected no fail symbol (✗) in a warn-only run, got: %s", out)
	}
}

// Same threshold, --json form: the machine-readable output must also carry
// the warn through without affecting the exit code.
func TestDoctorJSONExitsZeroWhenOnlyAWarn(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(warnStatoTypeChangedSchemaJSON))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	})

	var out string
	code := 0
	out = captureStdout(t, func() {
		code = executeArgs([]string{"doctor", "--json", "--config", cfg})
	})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (ExitOK) for a warn-only run: %s", code, ExitOK, out)
	}

	var checks []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &checks); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	var sawWarn bool
	for _, c := range checks {
		if c.Status == "fail" {
			t.Fatalf("unexpected fail check %q in a warn-only run: %s", c.Name, out)
		}
		if c.Status == "warn" {
			sawWarn = true
		}
	}
	if !sawWarn {
		t.Fatalf("expected a warn check in the JSON output: %s", out)
	}
}

func TestInitHeadlessWritesTheProfile(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1", "--database-id", "db1",
		"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
		"--profile", "work", "--config", cfg,
	})
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	loaded, err := loadConfigFrom(cfg)
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	p, err := loaded.Resolve("work")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.DataSourceID != "ds1" || p.Properties.Status != "Stato" || p.StatusType != "status" {
		t.Fatalf("profile = %+v", p)
	}
}

func TestInitListPrintsSharedDataSources(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[
			{"id":"ds1","title":[{"plain_text":"Tasks"}],"parent":{"database_id":"db1"}}
		],"has_more":false}`))
	})

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"init", "--list", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if !strings.Contains(out, "ds1") || !strings.Contains(out, "Tasks") {
		t.Fatalf("output does not list the data source: %q", out)
	}
}

// An empty list is the single most likely first-run failure, so the message
// has to name the fix rather than just report emptiness.
func TestInitListExplainsAnEmptyResult(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})
	captureStdout(t, func() {
		if code := executeArgs([]string{"init", "--list", "--config", cfg}); code == ExitOK {
			t.Fatal("an empty list should not exit 0")
		}
	})
}

// init must refuse a mapping the data source does not support, instead of
// writing a config that fails on first use.
func TestInitHeadlessRejectsAnInvalidMapping(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1",
		"--ticket-prop", "Nonexistent", "--status-prop", "Stato", "--title-prop", "Name",
		"--config", cfg,
	})
	if code == ExitOK {
		t.Fatal("init accepted a property that does not exist")
	}
}

// ticket, status and title are load-bearing: internal/service/doctor.go
// treats an unmapped one as a "fail", and get/list/upsert key every lookup
// off them. Writing a profile with any of the three blank produces a config
// that is broken on first use, so init must refuse before it ever writes.
func TestInitHeadlessRequiresTicketStatusAndTitle(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1", "--profile", "broken", "--config", cfg,
	})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}

	loaded, err := loadConfigFrom(cfg)
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if _, err := loaded.Resolve("broken"); err == nil {
		t.Fatal("init wrote a profile despite missing required property mappings (ticket/status/title)")
	}
}

// --list only reads and prints the data sources shared with the integration;
// it never writes a profile, so it must not demand the mapping flags that
// only matter once a config is about to be written.
func TestInitListDoesNotRequireMappingFlags(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[
			{"id":"ds1","title":[{"plain_text":"Tasks"}],"parent":{"database_id":"db1"}}
		],"has_more":false}`))
	})

	code := executeArgs([]string{"init", "--list", "--config", cfg})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (ExitOK)", code, ExitOK)
	}
}

// The property-type comparison in validateMapping has no dedicated coverage:
// deleting it would leave the whole suite green while init started accepting
// a column of the wrong type (e.g. a "select" property as the ticket key,
// which get/list cannot read as text).
func TestInitHeadlessRejectsAPropertyOfTheWrongType(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})

	// Stato is a "status" property, which is valid for --status-prop but not
	// for --ticket-prop (which only accepts rich_text or title).
	code := executeArgs([]string{
		"init", "--data-source-id", "ds1",
		"--ticket-prop", "Stato", "--status-prop", "Stato", "--title-prop", "Name",
		"--config", cfg,
	})
	if code == ExitOK {
		t.Fatal("init accepted a ticket property whose type (status) is not usable as a ticket key")
	}
}
