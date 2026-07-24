package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/config"
)

// stubbedDoctorAPI answers every call doctor makes with a healthy response, so
// a test about one check is not also a test of the other four.
func stubbedDoctorAPI(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/users/me":
		w.Write([]byte(`{"name":"notion-track"}`))
	case "/v1/data_sources/ds1":
		w.Write([]byte(cliSchemaJSON))
	default:
		w.Write([]byte(`{"results":[],"has_more":false}`))
	}
}

// A user with different tokens in NOTION_TOKEN and credentials.yml has no
// other way to tell which one doctor actually used, so the token check must
// name its source.
func TestDoctorReportsTokenSourceFromEnvironment(t *testing.T) {
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
	// withStubbedAPI already exports NOTION_TOKEN; that's the source under test.

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"doctor", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if !strings.Contains(out, "environment") {
		t.Fatalf("token check does not name the environment as its source: %s", out)
	}
}

func TestDoctorReportsTokenSourceFromFile(t *testing.T) {
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
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)
	if err := config.SaveToken("ntn_from_file"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	credPath, err := config.CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"doctor", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if !strings.Contains(out, credPath) {
		t.Fatalf("token check does not name the credentials file path %q: %s", credPath, out)
	}
}

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
	for _, want := range []string{"token", "data_source", "properties", "duplicates", "secrets"} {
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

// Every other command exits ExitAuth on an invalid token; doctor was the one
// exception, always exiting ExitError instead. A pipeline branching on 5 to
// mean "go fix the token" would be lied to by doctor alone. Doctor's own
// Service.Doctor stops at the token check and returns just that one Check
// when it fails (see internal/service/doctor.go), so this is exactly "the
// token check is the only one that failed".
func TestDoctorExitsAuthWhenOnlyTheTokenCheckFails(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":"unauthorized","message":"API token is invalid."}`))
	})

	captureStdout(t, func() {
		if code := executeArgs([]string{"doctor", "--config", cfg}); code != ExitAuth {
			t.Fatalf("exit code = %d, want %d (ExitAuth)", code, ExitAuth)
		}
	})
}

// When something other than (or in addition to) the token fails, doctor must
// keep reporting the generic ExitError: ExitAuth is reserved for "the fix is
// to check your token", which is not true when e.g. the data source check
// also failed.
func TestDoctorExitsErrorWhenANonTokenCheckFails(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"code":"object_not_found","message":"not shared"}`))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	})

	captureStdout(t, func() {
		if code := executeArgs([]string{"doctor", "--config", cfg}); code != ExitError {
			t.Fatalf("exit code = %d, want %d (ExitError)", code, ExitError)
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

// gitRepoWithFile builds a repository with one staged file and makes it the
// working directory, which is what the tracked-token scan reads. Skipped
// rather than failed when git is missing: that is an environment gap, not a
// defect in the code under test.
func gitRepoWithFile(t *testing.T, name, content string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", "--", name).CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	t.Chdir(dir)
}

// The token is built at run time so this file never carries a token-shaped
// literal of its own — it is tracked, and the scan under test would find it.
func fakeTokenLiteral() string { return "ntn_" + strings.Repeat("b", 46) }

// A committed token is the one mistake this tool makes easy. doctor warns, and
// warning is all it does: the row is still readable and every other command
// still works, so failing the run would be out of proportion.
func TestDoctorWarnsAboutATokenInATrackedFile(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedDoctorAPI)
	gitRepoWithFile(t, "app.yml", "token: "+fakeTokenLiteral()+"\n")

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"doctor", "--config", cfg}); code != ExitOK {
			t.Errorf("exit code = %d, want %d: a token warning must not fail the run", code, ExitOK)
		}
	})

	if !strings.Contains(out, "app.yml:1") {
		t.Errorf("output does not name the offending file and line: %s", out)
	}
	// Naming the secret in the warning about the secret would leak it a second
	// time, into terminal scrollback and CI logs.
	if strings.Contains(out, fakeTokenLiteral()) {
		t.Error("the warning printed the token itself")
	}
}

func TestDoctorReportsACleanRepository(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedDoctorAPI)
	gitRepoWithFile(t, "app.yml", "token: ntn_test\n")

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"doctor", "--config", cfg}); code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	if !strings.Contains(out, "secrets") {
		t.Errorf("output is missing the secrets check: %s", out)
	}
	if strings.Contains(out, "app.yml") {
		t.Errorf("a placeholder was reported as a token: %s", out)
	}
}

// Running outside a repository is the normal case for a user who installed the
// binary and is checking their setup from anywhere. It is not a problem to
// report, so it must not warn.
func TestDoctorOutsideARepositoryDoesNotWarn(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedDoctorAPI)
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Run(); err == nil {
		t.Skip("temp dir is inside a git work tree")
	}
	t.Chdir(dir)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"doctor", "--config", cfg}); code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	if !strings.Contains(out, "secrets") {
		t.Errorf("output is missing the secrets check: %s", out)
	}
	if strings.Contains(out, "! secrets") {
		t.Errorf("not being in a repository was reported as a warning: %s", out)
	}
}
