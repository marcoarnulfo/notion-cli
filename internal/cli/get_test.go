package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

// withStubbedAPI points the CLI at a fake Notion and a temp config file whose
// profile maps ticket and title to two different columns — the common case.
func withStubbedAPI(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	return withStubbedAPIProfile(t, handler, `schema_version: 1
default_profile: work
profiles:
  work:
    database_id: db1
    data_source_id: ds1
    status_type: status
    properties:
      ticket: Ticket
      status: Stato
      title: Name
`)
}

// withStubbedAPIProfile is withStubbedAPI with the profile spelled out, for the
// tests that need a mapping other than the default one.
func withStubbedAPIProfile(t *testing.T, handler http.HandlerFunc, configYAML string) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	oldClient := newClient
	newClient = func(token string) *notion.Client {
		return notion.New(token, notion.WithBaseURL(srv.URL))
	}
	t.Cleanup(func() { newClient = oldClient })

	t.Setenv(config.TokenEnv, "ntn_test")

	path := filepath.Join(t.TempDir(), "config.yml")
	os.WriteFile(path, []byte(configYAML), 0o600)
	return path
}

// titleKeyedProfile maps the ticket key and the title onto the same column, the
// way a board keyed by task name does.
const titleKeyedProfile = `schema_version: 1
default_profile: work
profiles:
  work:
    database_id: db1
    data_source_id: ds1
    status_type: status
    properties:
      ticket: Name
      status: Stato
      title: Name
`

const cliSchemaJSON = `{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{
	"Name":{"name":"Name","type":"title","title":{}},
	"Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
	"Stato":{"name":"Stato","type":"status","status":{"options":[{"name":"Fatto"}]}},
	"Referente":{"name":"Referente","type":"select","select":{"options":[{"name":"Andrea Ghidara"},{"name":"Marco Arnulfo"},{"name":"Mirko Spinato"}]}},
	"Urgenza":{"name":"Urgenza","type":"select","select":{"options":[{"name":"ALTA"},{"name":"MEDIA"},{"name":"NORMALE"}]}}}}`

const cliRowJSON = `{"id":"page1","url":"https://notion.so/page1",
	"last_edited_time":"2026-07-20T10:00:00.000Z","properties":{
	"Name":{"type":"title","title":[{"plain_text":"Hardening"}]},
	"Ticket":{"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
	"Stato":{"type":"status","status":{"name":"Fatto"}},
	"Referente":{"type":"select","select":{"name":"Mirko Spinato"}},
	"Urgenza":{"type":"select","select":{"name":"ALTA"}}}}`

// assigneeProfile maps the role and configures an identity, for the tests that
// exercise --assignee, --unassign and "me".
const assigneeProfile = `schema_version: 1
default_profile: work
profiles:
  work:
    database_id: db1
    data_source_id: ds1
    status_type: status
    me: Marco Arnulfo
    properties:
      ticket: Ticket
      status: Stato
      title: Name
      assignee: Referente
      priority: Urgenza
`

// assigneeProfileNoIdentity maps the role but says nothing about who "me" is:
// what a teammate who never exported NOTION_TRACK_ME has.
const assigneeProfileNoIdentity = `schema_version: 1
default_profile: work
profiles:
  work:
    database_id: db1
    data_source_id: ds1
    status_type: status
    properties:
      ticket: Ticket
      status: Stato
      title: Name
      assignee: Referente
      priority: Urgenza
`

func TestGetJSONPrintsAStableSchema(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	for _, key := range []string{"ticket", "page_id", "url", "status", "title", "last_edited_time"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing key %q in %v", key, got)
		}
	}
}

func TestGetMissingTicketExitsNotFound(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})

	if code := executeArgs([]string{"get", "--ticket", "NOPE", "--config", cfg}); code != ExitNotFound {
		t.Fatalf("exit code = %d, want %d", code, ExitNotFound)
	}
}

func TestGetWithoutTokenExitsAuth(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)

	if code := executeArgs([]string{"get", "--ticket", "X", "--config", cfg}); code != ExitAuth {
		t.Fatalf("exit code = %d, want %d", code, ExitAuth)
	}
}

// A credentials.yml the developer running these tests happens to have in
// their real home directory must never leak into a test that expects "no
// token found": withIsolatedUserConfigDir points os.UserConfigDir() (and so
// config.CredentialsPath) at an empty temp directory instead.
func withIsolatedUserConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("AppData", dir)
}

// An unreadable credentials.yml (here: a directory sitting where the file
// should be, which reproduces "exists but can't be read as YAML" without
// depending on how permissions behave for whoever runs the test suite) used
// to exit 1 like a generic failure. It is an authentication problem exactly
// like a missing or invalid token, so it must exit ExitAuth like the rest
// of them.
func TestGetWithUnreadableCredentialsExitsAuth(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)

	credPath, err := config.CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(credPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if code := executeArgs([]string{"get", "--ticket", "X", "--config", cfg}); code != ExitAuth {
		t.Fatalf("exit code = %d, want %d (ExitAuth)", code, ExitAuth)
	}
}

// A credentials.yml that exists, can be read, but fails to parse as YAML
// used to exit 1 like a generic failure, while an unreadable one (the test
// above) already exited ExitAuth. The same reasoning applies to both: there
// may be a token in there that nothing can prove exists.
func TestGetWithCorruptedCredentialsExitsAuth(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)

	credPath, err := config.CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath, []byte("not: [valid: yaml"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code := executeArgs([]string{"get", "--ticket", "X", "--config", cfg}); code != ExitAuth {
		t.Fatalf("exit code = %d, want %d (ExitAuth)", code, ExitAuth)
	}
}

// A token saved by init (or any prior session) must be picked up by every
// other command, not just init itself.
func TestGetUsesTokenFromCredentialsFile(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)
	if err := config.SaveToken("ntn_from_file"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
}

// captureStdout redirects os.Stdout for the duration of f.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	return captureStream(t, &os.Stdout, f)
}

// captureStderr is captureStdout for the other stream, for the output a
// command deliberately keeps off stdout so pipes stay clean.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	return captureStream(t, &os.Stderr, f)
}

// captureStream swaps a pipe in for *target while f runs and returns what was
// written to it. The root command reads os.Stdout and os.Stderr when it is
// built, which happens inside executeArgs — that is, inside f — so the swap
// reaches the command tree.
//
// The reader runs in its own goroutine: reading only after f returns would
// deadlock as soon as the output fills the pipe buffer, which a real
// `list --json` easily does. The restore is deferred so that a t.Fatalf inside
// f cannot leave the stream pointing at a closed pipe for every later test.
func captureStream(t *testing.T, target **os.File, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	old := *target
	*target = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()

	defer func() {
		*target = old
		w.Close()
	}()
	f()

	*target = old
	w.Close()
	return <-done
}

// The JSON is a public contract, so the values matter as much as the keys:
// swapping two fields in toPageJSON left the key-presence test green.
func TestGetJSONCarriesTheRightValues(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	want := map[string]string{
		"ticket":           "BDF-231",
		"title":            "Hardening",
		"status":           "Fatto",
		"page_id":          "page1",
		"url":              "https://notion.so/page1",
		"last_edited_time": "2026-07-20T10:00:00Z",
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Errorf("%s = %v, want %q", key, got[key], wantValue)
		}
	}
}

func TestListJSONReturnsAnArray(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"list", "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("output is not a JSON array: %s", out)
	}
	if len(rows) != 1 || rows[0]["ticket"] != "BDF-231" {
		t.Fatalf("rows = %v", rows)
	}
}

// No results must serialise as [], never null: a script doing `.[] | .ticket`
// breaks on null.
func TestListJSONWithNoResultsIsAnEmptyArrayNotNull(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"list", "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("output = %q, want []", strings.TrimSpace(out))
	}
}

// stubbedRow serves the schema on the schema endpoint and one row everywhere
// else, which is all the human-output tests below need from a fake Notion.
func stubbedRow(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/data_sources/ds1" {
		w.Write([]byte(cliSchemaJSON))
		return
	}
	w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
}

// A board keyed by task name maps ticket and title onto one column, so the
// human-readable row printed the same value twice, once per role. --json is
// unaffected: it has separate keys that legitimately carry the same value.
func TestListHumanOutputPrintsTheValueOnceWhenTicketIsTheTitle(t *testing.T) {
	cfg := withStubbedAPIProfile(t, stubbedRow, titleKeyedProfile)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"list", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	if n := strings.Count(out, "Hardening"); n != 1 {
		t.Errorf("value printed %d times, want 1: %q", n, out)
	}
	if !strings.Contains(out, "[Fatto]") {
		t.Errorf("status missing from %q", out)
	}
}

// The collapse must be keyed on the two roles sharing a column, not applied to
// every row: a profile that maps them apart still has two values to show.
func TestListHumanOutputKeepsBothValuesWhenTheyAreDifferentColumns(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedRow)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"list", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	for _, want := range []string{"BDF-231", "Hardening", "[Fatto]"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from %q", want, out)
		}
	}
}

func TestGetHumanOutputPrintsTheValueOnceWhenTicketIsTheTitle(t *testing.T) {
	cfg := withStubbedAPIProfile(t, stubbedRow, titleKeyedProfile)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "Hardening", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	if n := strings.Count(out, "Hardening"); n != 1 {
		t.Errorf("value printed %d times, want 1: %q", n, out)
	}
	for _, want := range []string{"[Fatto]", "https://notion.so/page1"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from %q", want, out)
		}
	}
}

func TestGetHumanOutputKeepsBothValuesWhenTheyAreDifferentColumns(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedRow)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	for _, want := range []string{"BDF-231", "Hardening", "[Fatto]", "https://notion.so/page1"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from %q", want, out)
		}
	}
}

// stubbedNoRows is stubbedRow for the empty case.
func stubbedNoRows(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/data_sources/ds1" {
		w.Write([]byte(cliSchemaJSON))
		return
	}
	w.Write([]byte(`{"results":[],"has_more":false}`))
}

// Printing nothing at all left the user unable to tell a working command with
// no matches from one that silently did nothing. The line goes to stderr so
// that stdout stays empty and `list | wc -l` keeps counting rows, not notices.
func TestListHumanOutputReportsAnEmptyResultOnStderr(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedNoRows)

	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if code := executeArgs([]string{"list", "--config", cfg}); code != ExitOK {
				t.Errorf("exit code = %d, want %d", code, ExitOK)
			}
		})
	})

	if !strings.Contains(stderr, "no matching tasks") {
		t.Errorf("stderr = %q, want it to report the empty result", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want it left empty for pipes", stdout)
	}
}

// The notice is for a human reading a terminal. A script asking for --json
// gets [] and must not find prose on either stream.
func TestListJSONWithNoResultsStaysSilentOnStderr(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedNoRows)

	stderr := captureStderr(t, func() {
		captureStdout(t, func() {
			if code := executeArgs([]string{"list", "--json", "--config", cfg}); code != ExitOK {
				t.Errorf("exit code = %d, want %d", code, ExitOK)
			}
		})
	})

	if stderr != "" {
		t.Errorf("stderr = %q, want it empty for --json", stderr)
	}
}

func TestListRejectsAnUnknownStatusBeforeQuerying(t *testing.T) {
	var queried bool
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		queried = true
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})

	if code := executeArgs([]string{"list", "--status", "Nope", "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if queried {
		t.Fatal("an unknown status reached the API")
	}
}

// stubForGet answers schema and query with the shared fixtures.
func stubForGet(t *testing.T, profile string) string {
	t.Helper()
	return withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	}, profile)
}

func TestGetJSONCarriesTheAssignee(t *testing.T) {
	cfg := stubForGet(t, assigneeProfile)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{
			"get", "--ticket", "BDF-231", "--json", "--config", cfg,
		}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	var row map[string]any
	if err := json.Unmarshal([]byte(out), &row); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	if row["assignee"] != "Mirko Spinato" {
		t.Errorf("assignee = %v, want %q", row["assignee"], "Mirko Spinato")
	}
}

func TestGetJSONAlwaysCarriesTheAssigneeKey(t *testing.T) {
	// A script reading .assignee must not have to branch on the key existing,
	// so it is present even for a profile that never mapped the role.
	cfg := withStubbedAPI(t, stubbedRow)

	out := captureStdout(t, func() {
		executeArgs([]string{"get", "--ticket", "BDF-231", "--json", "--config", cfg})
	})

	var row map[string]any
	if err := json.Unmarshal([]byte(out), &row); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	assignee, ok := row["assignee"]
	if !ok {
		t.Fatalf("the assignee key is missing from %v", row)
	}
	if assignee != "" {
		t.Errorf("assignee = %v, want %q", assignee, "")
	}
}

func TestGetHumanOutputShowsTheAssignee(t *testing.T) {
	cfg := stubForGet(t, assigneeProfile)

	out := captureStdout(t, func() {
		executeArgs([]string{"get", "--ticket", "BDF-231", "--config", cfg})
	})

	if !strings.Contains(out, "@Mirko Spinato") {
		t.Errorf("output = %q, want it to name the assignee", out)
	}
}

func TestGetHumanOutputIsUnchangedWithoutTheRole(t *testing.T) {
	// Non-regression: a profile written before this feature must print exactly
	// what it printed before, down to the byte.
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})

	out := captureStdout(t, func() {
		executeArgs([]string{"get", "--ticket", "BDF-231", "--config", cfg})
	})

	want := "BDF-231  Hardening  [Fatto]\n  https://notion.so/page1\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestGetJSONCarriesThePriority(t *testing.T) {
	cfg := stubForGet(t, assigneeProfile)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{
			"get", "--ticket", "BDF-231", "--json", "--config", cfg,
		}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	var row map[string]any
	if err := json.Unmarshal([]byte(out), &row); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	if row["priority"] != "ALTA" {
		t.Errorf("priority = %v, want %q", row["priority"], "ALTA")
	}
}

func TestGetHumanOutputShowsPriorityBeforeTheAssignee(t *testing.T) {
	cfg := stubForGet(t, assigneeProfile)

	out := captureStdout(t, func() {
		executeArgs([]string{"get", "--ticket", "BDF-231", "--config", cfg})
	})

	if !strings.Contains(out, "!ALTA  @Mirko Spinato") {
		t.Errorf("output = %q, want the priority before the assignee", out)
	}
}

// cliRowNoPriorityJSON is the shared row with Urgenza empty: the case where one
// of the two trailing segments is there and the other is not, which is where a
// stray separator or an orphan sigil would hide.
const cliRowNoPriorityJSON = `{"id":"page1","url":"https://notion.so/page1",
	"last_edited_time":"2026-07-20T10:00:00.000Z","properties":{
	"Name":{"type":"title","title":[{"plain_text":"Hardening"}]},
	"Ticket":{"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
	"Stato":{"type":"status","status":{"name":"Fatto"}},
	"Referente":{"type":"select","select":{"name":"Mirko Spinato"}},
	"Urgenza":{"type":"select","select":null}}}`

func TestGetHumanOutputWithOnlyOneOfTheTwoSegments(t *testing.T) {
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowNoPriorityJSON + `],"has_more":false}`))
	}, assigneeProfile)

	out := captureStdout(t, func() {
		executeArgs([]string{"get", "--ticket", "BDF-231", "--config", cfg})
	})

	want := "BDF-231  Hardening  [Fatto]  @Mirko Spinato\n  https://notion.so/page1\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestGetJSONCarriesTheBoardID(t *testing.T) {
	page := notion.Page{
		ID:  "page1",
		URL: "https://notion.so/page1",
		Properties: map[string]notion.PropertyValue{
			"ID":   {Type: "unique_id", Text: "BDF-271"},
			"Name": {Type: "title", Text: "Hardening"},
		},
	}
	got := toPageJSON(page, config.Properties{ID: "ID", Title: "Name"})
	if got.ID != "BDF-271" {
		t.Errorf("ID = %q, want %q", got.ID, "BDF-271")
	}
}

func TestGetJSONKeepsTheIDKeyWhenTheRoleIsUnmapped(t *testing.T) {
	// The key is always present so no script has to branch on it — the same
	// rule assignee and priority already follow.
	b, err := json.Marshal(toPageJSON(notion.Page{ID: "page1"}, config.Properties{}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"id":""`) {
		t.Errorf("marshalled to %s, want an empty id key", b)
	}
}

// idProfileYAML maps the id role on top of the common profile.
const idProfileYAML = `schema_version: 1
default_profile: work
profiles:
  work:
    database_id: db1
    data_source_id: ds1
    status_type: status
    properties:
      ticket: Ticket
      status: Stato
      title: Name
      id: ID
`

// cliRowWithIDJSON is a row carrying a board id.
const cliRowWithIDJSON = `{"id":"23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5",
	"url":"https://notion.so/23fb4e5c8a5f4d21b7c9d0e1f2a3b4c5",
	"last_edited_time":"2026-07-20T10:00:00.000Z",
	"parent":{"type":"data_source_id","data_source_id":"ds1"},
	"properties":{
	"Name":{"type":"title","title":[{"plain_text":"Hardening"}]},
	"Ticket":{"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
	"Stato":{"type":"status","status":{"name":"Fatto"}},
	"ID":{"type":"unique_id","unique_id":{"prefix":"BDF","number":271}}}}`

func TestGetByBoardIDReadsTheRow(t *testing.T) {
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaWithIDJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowWithIDJSON + `],"has_more":false}`))
	}, idProfileYAML)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--id", "BDF-271", "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	if got["id"] != "BDF-271" {
		t.Errorf("id = %v, want %q", got["id"], "BDF-271")
	}
}

func TestGetRejectsTwoWaysOfAddressingAtOnce(t *testing.T) {
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaWithIDJSON))
	}, idProfileYAML)

	if code := executeArgs([]string{
		"get", "--id", "BDF-271", "--ticket", "Hardening", "--config", cfg,
	}); code != ExitUsage {
		t.Errorf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}

func TestGetRejectsAnEmptyBoardID(t *testing.T) {
	// Passed but blank: cobra sees a flag, the service sees no value. It must
	// take the id path anyway and surface ErrEmptyID, not fall through to a
	// ticket lookup with a key it was never given.
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaWithIDJSON))
	}, idProfileYAML)

	var code int
	errOut := captureStderr(t, func() {
		code = executeArgs([]string{"get", "--id", "", "--config", cfg})
	})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
	// The exit code alone proves nothing here: the regression this test exists
	// for — branching on the value instead of on Changed("id") — falls through
	// to a ticket lookup, raises ErrEmptyTicket, and exits 2 as well. The
	// message is what tells the two apart.
	if !strings.Contains(errOut, "id must not be empty") {
		t.Errorf("stderr = %q, want it to report an empty id", errOut)
	}
}

func TestGetPrintsTheBoardIDForHumans(t *testing.T) {
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaWithIDJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowWithIDJSON + `],"has_more":false}`))
	}, idProfileYAML)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if !strings.Contains(out, "BDF-271") {
		t.Errorf("output = %q, want it to show the board id", out)
	}
}

func TestGetHumanOutputIsUnchangedWithoutTheIDRole(t *testing.T) {
	// The profile from withStubbedAPI maps no id role: the line must come out
	// byte-identical to what it was before this feature existed, which is the
	// rule the two suffixes in output.go already follow (see
	// TestGetHumanOutputIsUnchangedWithoutTheRole above, pinned the same way
	// for the assignee/priority suffixes when this feature predates them).
	//
	// An exact match, not just "no leading space": that weaker check would
	// still pass if idPrefix ever emitted a non-empty prefix that happened
	// not to start with a space (e.g. a stray "id:" with no padding), which
	// is exactly the kind of regression this test exists to catch.
	cfg := withStubbedAPI(t, stubbedRow)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	want := "BDF-231  Hardening  [Fatto]\n  https://notion.so/page1\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}
