package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	if code := executeArgs([]string{"get", "--ticket", "X", "--config", cfg}); code != ExitAuth {
		t.Fatalf("exit code = %d, want %d", code, ExitAuth)
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
