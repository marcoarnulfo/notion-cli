package cli

import (
	"encoding/json"
	"net/http"
	"testing"
)

// testPageID/testPageIDCanonical mirror the notion package's normalization:
// the server must see the canonical dashed form regardless of which shape
// was passed on the command line.
const (
	testPageID          = "23fb4e5c8a5f4d21b7c9d0e1f2a3b4c5"
	testPageIDCanonical = "23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5"
)

const cliRowJSONWithParent = `{"id":"23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5","url":"https://notion.so/page1",
	"last_edited_time":"2026-07-20T10:00:00.000Z","parent":{"type":"data_source_id","data_source_id":"ds1"},"properties":{
	"Name":{"type":"title","title":[{"plain_text":"Hardening"}]},
	"Ticket":{"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
	"Stato":{"type":"status","status":{"name":"Fatto"}}}}`

const cliRowJSONOtherDataSource = `{"id":"23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5","url":"https://notion.so/page1",
	"last_edited_time":"2026-07-20T10:00:00.000Z","parent":{"type":"data_source_id","data_source_id":"ds-other"},"properties":{}}`

func containsRequest(seen []string, want string) bool {
	for _, s := range seen {
		if s == want {
			return true
		}
	}
	return false
}

func TestGetByPageIDReadsThePageDirectly(t *testing.T) {
	var seen []string
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Write([]byte(cliRowJSONWithParent))
	})

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--page-id", testPageID, "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	if got["ticket"] != "BDF-231" {
		t.Fatalf("ticket = %v", got["ticket"])
	}
	if !containsRequest(seen, "GET /v1/pages/"+testPageIDCanonical) {
		t.Fatalf("did not read the page directly: %v", seen)
	}
	if containsRequest(seen, "POST /v1/data_sources/ds1/query") {
		t.Fatal("get --page-id must not query by key")
	}
}

func TestSetByPageIDUpdatesWithoutQuerying(t *testing.T) {
	var seen []string
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(cliRowJSONWithParent))
	})

	out := captureStdout(t, func() {
		code := executeArgs([]string{"set", "--page-id", testPageID, "--status", "Fatto", "--json", "--config", cfg})
		if code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	if got["action"] != "updated" {
		t.Fatalf("action = %v", got["action"])
	}
	if !containsRequest(seen, "PATCH /v1/pages/"+testPageIDCanonical) {
		t.Fatalf("did not patch the page directly: %v", seen)
	}
	if containsRequest(seen, "POST /v1/data_sources/ds1/query") {
		t.Fatal("set --page-id must not query by key")
	}
}

func TestGetRejectsTicketAndPageIDTogether(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {})
	code := executeArgs([]string{"get", "--ticket", "BDF-231", "--page-id", testPageID, "--config", cfg})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}

func TestGetRequiresTicketOrPageID(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {})
	if code := executeArgs([]string{"get", "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}

func TestSetRejectsTicketAndPageIDTogether(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {})
	code := executeArgs([]string{"set", "--ticket", "BDF-231", "--page-id", testPageID, "--status", "Fatto", "--config", cfg})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}

func TestSetRequiresTicketOrPageID(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {})
	if code := executeArgs([]string{"set", "--status", "Fatto", "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}

func TestGetByPageIDRejectsAPageOutsideTheProfile(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliRowJSONOtherDataSource))
	})
	if code := executeArgs([]string{"get", "--page-id", testPageID, "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}

func TestGetByPageIDExitsNotFoundOn404(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":"object_not_found","message":"not found"}`))
	})
	if code := executeArgs([]string{"get", "--page-id", testPageID, "--config", cfg}); code != ExitNotFound {
		t.Fatalf("exit code = %d, want %d", code, ExitNotFound)
	}
}

func TestGetByPageIDRejectsAMalformedIDBeforeAnyRequest(t *testing.T) {
	var queried bool
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		queried = true
		w.Write([]byte(cliRowJSONWithParent))
	})
	if code := executeArgs([]string{"get", "--page-id", "not-a-page-id", "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if queried {
		t.Fatal("a malformed page id reached the API")
	}
}

// upsert always creates by ticket key; a page id cannot exist before the row
// does, so upsert must not accept the flag at all.
func TestUpsertDoesNotAcceptPageID(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {})
	code := executeArgs([]string{"upsert", "--ticket", "BDF-1", "--page-id", testPageID, "--config", cfg})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}
