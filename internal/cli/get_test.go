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

// withStubbedAPI points the CLI at a fake Notion and a temp config file.
func withStubbedAPI(t *testing.T, handler http.HandlerFunc) string {
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
	os.WriteFile(path, []byte(`schema_version: 1
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
`), 0o600)
	return path
}

const cliSchemaJSON = `{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{
	"Name":{"name":"Name","type":"title","title":{}},
	"Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
	"Stato":{"name":"Stato","type":"status","status":{"options":[{"name":"Fatto"}]}}}}`

const cliRowJSON = `{"id":"page1","url":"https://notion.so/page1",
	"last_edited_time":"2026-07-20T10:00:00.000Z","properties":{
	"Name":{"type":"title","title":[{"plain_text":"Hardening"}]},
	"Ticket":{"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
	"Stato":{"type":"status","status":{"name":"Fatto"}}}}`

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
//
// The reader runs in its own goroutine: reading only after f returns would
// deadlock as soon as the output fills the pipe buffer, which a real
// `list --json` easily does. The restore is deferred so that a t.Fatalf inside
// f cannot leave os.Stdout pointing at a closed pipe for every later test.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	old := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()

	defer func() {
		os.Stdout = old
		w.Close()
	}()
	f()

	os.Stdout = old
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
