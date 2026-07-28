package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

const schemaJSON = `{
  "id":"ds1","title":[{"plain_text":"Tasks"}],
  "properties":{
    "Name":{"name":"Name","type":"title","title":{}},
    "Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
    "Stato":{"name":"Stato","type":"status","status":{"options":[{"name":"In corso"},{"name":"Fatto"}]}},
    "Referente":{"name":"Referente","type":"select","select":{"options":[{"name":"Andrea Ghidara"},{"name":"Marco Arnulfo"},{"name":"Mirko Spinato"}]}},
    "Urgenza":{"name":"Urgenza","type":"select","select":{"options":[{"name":"ALTA"},{"name":"MEDIA"},{"name":"NORMALE"}]}}
  }}`

const rowJSON = `{
  "id":"page1","url":"https://notion.so/page1","last_edited_time":"2026-07-20T10:00:00.000Z",
  "parent":{"type":"data_source_id","data_source_id":"ds1"},
  "properties":{
    "Name":{"type":"title","title":[{"plain_text":"Hardening"}]},
    "Ticket":{"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
    "Stato":{"type":"status","status":{"name":"In corso"}},
    "Referente":{"type":"select","select":{"name":"Mirko Spinato"}},
    "Urgenza":{"type":"select","select":{"name":"ALTA"}}
  }}`

func testProfile() config.Profile {
	return config.Profile{
		DatabaseID:   "db1",
		DataSourceID: "ds1",
		StatusType:   "status",
		Properties:   config.Properties{Ticket: "Ticket", Status: "Stato", Title: "Name"},
	}
}

// assigneeProfile is testProfile with the role mapped, and an optional identity.
func assigneeProfile(me string) config.Profile {
	p := testProfile()
	p.Properties.Assignee = "Referente"
	p.Me = me
	return p
}

// priorityProfile is assigneeProfile with the priority role mapped too, which
// is what the real board looks like.
func priorityProfile() config.Profile {
	p := assigneeProfile("")
	p.Properties.Priority = "Urgenza"
	return p
}

// capturingRoutes is routes() plus a copy of the properties payload of the last
// write: an assignee test asserts on what was sent, not on what came back.
func capturingRoutes(t *testing.T, queryResults string, written *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/pages" ||
			r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v1/pages/") {
			var body struct {
				Properties map[string]any `json:"properties"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding the write payload: %v", err)
			}
			*written = body.Properties
		}
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + queryResults + `],"has_more":false}`))
		default:
			w.Write([]byte(rowJSON))
		}
	}))
}

// routes returns a server answering schema reads, queries, creates and updates
// with whatever the test supplies for the query result.
func routes(t *testing.T, queryResults string, seen *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + queryResults + `],"has_more":false}`))
		case r.URL.Path == "/v1/pages":
			w.Write([]byte(rowJSON))
		default: // PATCH /v1/pages/{id}
			w.Write([]byte(rowJSON))
		}
	}))
}

// bodyRoutes answers schema/query/create/update plus the three block endpoints,
// recording "METHOD path" order so a test can assert snapshot→append→delete.
func bodyRoutes(t *testing.T, queryResults, children string, seen *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + queryResults + `],"has_more":false}`))
		case r.URL.Path == "/v1/pages":
			w.Write([]byte(rowJSON))
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == http.MethodGet:
			w.Write([]byte(`{"results":[` + children + `],"has_more":false}`))
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == http.MethodPatch:
			w.Write([]byte(`{}`))
		default: // PATCH /v1/pages/{id}, DELETE /v1/blocks/{id}
			w.Write([]byte(rowJSON))
		}
	}))
}

func TestReplaceBodyOrdersSnapshotAppendDelete(t *testing.T) {
	var seen []string
	srv := bodyRoutes(t, rowJSON, `{"id":"old1","type":"paragraph"}`, &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL), notion.WithSleep(func(time.Duration) {})), testProfile())
	body := &BodyRequest{Blocks: []notion.Block{{Type: "paragraph", RichText: []notion.Span{{Content: "new"}}}}}
	res, err := s.Set(context.Background(), tracker.Fields{Ticket: "BDF-231"}, body)
	if err != nil {
		t.Fatalf("Set with body: %v", err)
	}
	// Assert relative order: children GET (snapshot) before children PATCH
	// (append) before /v1/blocks/old1 DELETE.
	iGet := indexOf(seen, "GET", "/children")
	iPatch := indexOf(seen, "PATCH", "/children")
	iDel := indexOf(seen, "DELETE", "/blocks/old1")
	if !(iGet >= 0 && iGet < iPatch && iPatch < iDel) {
		t.Fatalf("order wrong: %v (get=%d patch=%d del=%d)", seen, iGet, iPatch, iDel)
	}
	if res.Body == nil || res.Body.BlocksWritten != 1 || res.Body.BlocksDeleted != 1 {
		t.Fatalf("body result = %+v", res.Body)
	}
}

func TestReplaceBodySkipsChildPageOnDelete(t *testing.T) {
	var seen []string
	srv := bodyRoutes(t, rowJSON, `{"id":"sub1","type":"child_page"}`, &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL), notion.WithSleep(func(time.Duration) {})), testProfile())
	res, err := s.Set(context.Background(), tracker.Fields{Ticket: "BDF-231"},
		&BodyRequest{Blocks: []notion.Block{{Type: "paragraph", RichText: []notion.Span{{Content: "x"}}}}})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if indexOf(seen, "DELETE", "/blocks/sub1") >= 0 {
		t.Fatal("child_page must NOT be deleted")
	}
	if len(res.Body.Warnings) == 0 {
		t.Fatal("skipping a child_page must produce a warning")
	}
}

func TestUpsertWithNilBodyLeavesBodyUntouched(t *testing.T) {
	var seen []string
	srv := bodyRoutes(t, "", "", &seen)
	defer srv.Close()
	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	if _, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231"}, nil); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	for _, e := range seen {
		if strings.Contains(e, "/children") || strings.Contains(e, "/blocks/") {
			t.Fatalf("nil body must touch no block endpoint, saw %q", e)
		}
	}
}

func TestSetBodyAppendFailureKeepsPropertiesApplied(t *testing.T) {
	// The children PATCH (append) 400s. Properties were already written, so
	// the caller must get a *BodyWriteError with the page still populated.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + rowJSON + `],"has_more":false}`))
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == http.MethodGet:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"code":"validation_error","message":"bad block"}`))
		default: // PATCH /v1/pages/{id}
			w.Write([]byte(rowJSON))
		}
	}))
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL), notion.WithSleep(func(time.Duration) {})), testProfile())
	res, err := s.Set(context.Background(), tracker.Fields{Ticket: "BDF-231"},
		&BodyRequest{Blocks: []notion.Block{{Type: "paragraph", RichText: []notion.Span{{Content: "x"}}}}})
	var bwe *BodyWriteError
	if !errors.As(err, &bwe) {
		t.Fatalf("append failure must be a *BodyWriteError, got %v", err)
	}
	if res.Page.ID == "" {
		t.Fatal("properties were written, so Result.Page must be populated")
	}
	if res.Action != "updated" {
		t.Fatalf("action = %q, want updated", res.Action)
	}
	if res.Body == nil {
		t.Fatal("res.Body must be populated even on append failure: the --json partial-failure contract depends on it")
	}
}

// indexOf returns the position of the first "METHOD …suffix" entry, or -1.
func indexOf(seen []string, method, suffix string) int {
	for i, e := range seen {
		if strings.HasPrefix(e, method+" ") && strings.Contains(e, suffix) {
			return i
		}
	}
	return -1
}

func TestUpsertCreatesWhenNoRowMatches(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	got, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231", Status: "Fatto"}, nil)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got.Action != "created" {
		t.Fatalf("action = %q, want created", got.Action)
	}
	if !contains(seen, "POST /v1/pages") {
		t.Fatalf("no page was created: %v", seen)
	}
}

func TestUpsertUpdatesWhenOneRowMatches(t *testing.T) {
	var seen []string
	srv := routes(t, rowJSON, &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	got, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231", Status: "Fatto"}, nil)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got.Action != "updated" {
		t.Fatalf("action = %q, want updated", got.Action)
	}
	if !contains(seen, "PATCH /v1/pages/page1") {
		t.Fatalf("no page was updated: %v", seen)
	}
}

func TestUpsertIsIdempotent(t *testing.T) {
	var seen []string
	srv := routes(t, rowJSON, &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	f := tracker.Fields{Ticket: "BDF-231", Status: "Fatto"}
	for i := 0; i < 2; i++ {
		if _, err := s.Upsert(context.Background(), f, nil); err != nil {
			t.Fatalf("Upsert %d: %v", i, err)
		}
	}
	if contains(seen, "POST /v1/pages") {
		t.Fatal("a second run created a row instead of updating it")
	}
}

func TestUpsertFailsOnDuplicates(t *testing.T) {
	var seen []string
	srv := routes(t, rowJSON+","+rowJSON, &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231"}, nil)
	var dup *tracker.DuplicateError
	if !errors.As(err, &dup) {
		t.Fatalf("got %v, want *tracker.DuplicateError", err)
	}
}

// cmd.MarkFlagRequired only checks that --ticket was passed, not that it
// carries a value: `upsert --ticket ""` used to sail through with an empty
// key. BuildProperties then silently drops the ticket property (add()
// treats "" as "leave this alone"), so the row it creates has no ticket
// value at all — unreachable by any future get/set/upsert. Rejecting the
// empty key here, before any request is made, is what closes that hole for
// every caller (CLI, TUI, a future MCP adapter) at once.
func TestUpsertRejectsEmptyTicket(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "", Title: "Ghost"}, nil)
	if !errors.Is(err, ErrEmptyTicket) {
		t.Fatalf("got %v, want ErrEmptyTicket", err)
	}
	if len(seen) != 0 {
		t.Fatalf("an empty ticket reached the API: %v", seen)
	}
}

func TestSetRejectsEmptyTicket(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.Set(context.Background(), tracker.Fields{Ticket: "", Status: "Fatto"}, nil)
	if !errors.Is(err, ErrEmptyTicket) {
		t.Fatalf("got %v, want ErrEmptyTicket", err)
	}
	if len(seen) != 0 {
		t.Fatalf("an empty ticket reached the API: %v", seen)
	}
}

func TestGetRejectsEmptyTicket(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.Get(context.Background(), "")
	if !errors.Is(err, ErrEmptyTicket) {
		t.Fatalf("got %v, want ErrEmptyTicket", err)
	}
	if len(seen) != 0 {
		t.Fatalf("an empty ticket reached the API: %v", seen)
	}
}

func TestSetFailsWhenTheRowDoesNotExist(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.Set(context.Background(), tracker.Fields{Ticket: "BDF-999", Status: "Fatto"}, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if contains(seen, "POST /v1/pages") {
		t.Fatal("set created a row; only upsert may do that")
	}
}

// Get re-implemented the same 0/1/N choice tracker.Decide already makes for
// Upsert, instead of calling it. There was no coverage pinning Get's
// duplicate behaviour specifically, so this guards against the two
// diverging — e.g. a future edit to Decide's DuplicateError shape silently
// not reaching Get because Get never calls it.
func TestGetFailsOnDuplicates(t *testing.T) {
	var seen []string
	srv := routes(t, rowJSON+","+rowJSON, &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.Get(context.Background(), "BDF-231")
	var dup *tracker.DuplicateError
	if !errors.As(err, &dup) {
		t.Fatalf("got %v, want *tracker.DuplicateError", err)
	}
}

func TestListFiltersByStatus(t *testing.T) {
	var gotFilter map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(schemaJSON))
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotFilter, _ = body["filter"].(map[string]any)
		w.Write([]byte(`{"results":[` + rowJSON + `],"has_more":false}`))
	}))
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	got, err := s.List(context.Background(), ListFilter{Status: "In corso"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if gotFilter["property"] != "Stato" {
		t.Fatalf("filter = %v", gotFilter)
	}
}

// filterRoutes answers schema and query, keeping the raw filter of the last
// query so a test can assert on the request rather than on canned rows.
func filterRoutes(t *testing.T, sent *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1/query" {
			var body struct {
				Filter map[string]any `json:"filter"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding the query: %v", err)
			}
			*sent = body.Filter
			w.Write([]byte(`{"results":[],"has_more":false}`))
			return
		}
		w.Write([]byte(schemaJSON))
	}))
}

func TestListFilters(t *testing.T) {
	var sent map[string]any
	srv := filterRoutes(t, &sent)
	defer srv.Close()
	client := notion.New("t", notion.WithBaseURL(srv.URL))
	s := New(client, assigneeProfile("Marco Arnulfo"))
	ctx := context.Background()

	t.Run("no filter at all", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{}); err != nil {
			t.Fatalf("List: %v", err)
		}
		if sent != nil {
			t.Errorf("filter = %#v, want none so every row comes back", sent)
		}
	})

	t.Run("status only is unchanged", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{Status: "Fatto"}); err != nil {
			t.Fatalf("List: %v", err)
		}
		if sent["property"] != "Stato" {
			t.Errorf("filter = %#v, want the plain status filter", sent)
		}
	})

	t.Run("assignee compounds with status", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{Status: "Fatto", Assignee: "mirko"}); err != nil {
			t.Fatalf("List: %v", err)
		}
		clauses, ok := sent["and"].([]any)
		if !ok || len(clauses) != 2 {
			t.Fatalf("filter = %#v, want a compound of two", sent)
		}
		// Which two, not just how many: a compound of the status clause twice
		// would satisfy a count and return the wrong rows.
		byProperty := map[string]any{}
		for _, c := range clauses {
			clause := c.(map[string]any)
			byProperty[clause["property"].(string)] = clause
		}
		status, ok := byProperty["Stato"].(map[string]any)
		if !ok {
			t.Fatalf("no clause on Stato in %#v", clauses)
		}
		if got := status["status"].(map[string]any)["equals"]; got != "Fatto" {
			t.Errorf("status clause = %v, want Fatto", got)
		}
		assignee, ok := byProperty["Referente"].(map[string]any)
		if !ok {
			t.Fatalf("no clause on Referente in %#v", clauses)
		}
		if got := assignee["select"].(map[string]any)["equals"]; got != "Mirko Spinato" {
			t.Errorf("assignee clause = %v, want the canonical option", got)
		}
	})

	t.Run("a partial name is resolved before it is sent", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{Assignee: "mirko"}); err != nil {
			t.Fatalf("List: %v", err)
		}
		got := sent["select"].(map[string]any)["equals"]
		if got != "Mirko Spinato" {
			t.Errorf("filter value = %v, want the canonical option", got)
		}
	})

	t.Run("me resolves in a filter too", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{Assignee: "me"}); err != nil {
			t.Fatalf("List: %v", err)
		}
		got := sent["select"].(map[string]any)["equals"]
		if got != "Marco Arnulfo" {
			t.Errorf("filter value = %v, want the configured identity", got)
		}
	})

	t.Run("unassigned", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{Unassigned: true}); err != nil {
			t.Fatalf("List: %v", err)
		}
		if got := sent["select"].(map[string]any)["is_empty"]; got != true {
			t.Errorf("filter = %#v, want is_empty", sent)
		}
	})

	t.Run("assignee and unassigned together is a conflict", func(t *testing.T) {
		_, err := s.List(ctx, ListFilter{Assignee: "mirko", Unassigned: true})
		if !errors.Is(err, ErrConflictingListFilter) {
			t.Fatalf("error = %v, want ErrConflictingListFilter", err)
		}
	})

	t.Run("filtering on an unmapped role fails clearly", func(t *testing.T) {
		unmapped := New(client, testProfile())
		if _, err := unmapped.List(ctx, ListFilter{Assignee: "mirko"}); err == nil {
			t.Fatal("List = nil error, want a failure naming --assignee-prop")
		}
	})
}

func TestListFiltersByPriority(t *testing.T) {
	var sent map[string]any
	srv := filterRoutes(t, &sent)
	defer srv.Close()
	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), priorityProfile())
	ctx := context.Background()

	t.Run("alone", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{Priority: "alta"}); err != nil {
			t.Fatalf("List: %v", err)
		}
		if sent["property"] != "Urgenza" {
			t.Fatalf("filter = %#v, want it on Urgenza", sent)
		}
		if got := sent["select"].(map[string]any)["equals"]; got != "ALTA" {
			t.Errorf("filter value = %v, want the canonical option", got)
		}
	})

	t.Run("three clauses compound", func(t *testing.T) {
		sent = nil
		_, err := s.List(ctx, ListFilter{Status: "Fatto", Assignee: "mirko", Priority: "ALTA"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		clauses, ok := sent["and"].([]any)
		if !ok || len(clauses) != 3 {
			t.Fatalf("filter = %#v, want a compound of three", sent)
		}
		// Which three, not just how many.
		byProperty := map[string]bool{}
		for _, c := range clauses {
			byProperty[c.(map[string]any)["property"].(string)] = true
		}
		for _, want := range []string{"Stato", "Referente", "Urgenza"} {
			if !byProperty[want] {
				t.Errorf("no clause on %s in %#v", want, clauses)
			}
		}
	})

	t.Run("filtering on an unmapped role fails clearly", func(t *testing.T) {
		unmapped := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile(""))
		if _, err := unmapped.List(ctx, ListFilter{Priority: "ALTA"}); err == nil {
			t.Fatal("List = nil error, want a failure naming --priority-prop")
		}
	})
}

// The schema is read once per Service, not once per call.
func TestSchemaIsCached(t *testing.T) {
	var seen []string
	srv := routes(t, rowJSON, &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	ctx := context.Background()
	s.Get(ctx, "BDF-231")
	s.Get(ctx, "BDF-231")

	n := 0
	for _, r := range seen {
		if r == "GET /v1/data_sources/ds1" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("schema was fetched %d times, want 1", n)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// The TUI runs commands on separate goroutines against one Service, so the
// lazily cached schema must not race.
func TestSchemaCacheIsSafeForConcurrentUse(t *testing.T) {
	// A dedicated handler rather than routes(): that helper appends to a shared
	// slice, which would itself race under concurrent requests.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(schemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + rowJSON + `],"has_more":false}`))
	}))
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Get(ctx, "BDF-231")
		}()
	}
	wg.Wait()
}

func TestUpsertResolvesAssignee(t *testing.T) {
	var written map[string]any
	srv := capturingRoutes(t, "", &written)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile(""))
	_, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231", Assignee: "mirko"}, nil)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, _ := json.Marshal(written["Referente"])
	if want := `{"select":{"name":"Mirko Spinato"}}`; string(got) != want {
		t.Errorf("Referente = %s, want %s", got, want)
	}
}

func TestSetByIDResolvesAssignee(t *testing.T) {
	// The third write path is the one a refactor forgets: it never queries by
	// ticket, so it does not share Set's code.
	var written map[string]any
	srv := capturingRoutes(t, rowJSON, &written)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile(""))
	// testPageID (defined in pageid_test.go), not the literal "page1" the row
	// JSON carries as its id: SetByID's pageID argument is what NormalizePageID
	// validates, and "page1" is not one of its three accepted shapes.
	_, err := s.SetByID(context.Background(), testPageID, tracker.Fields{Assignee: "mirko"}, nil)
	if err != nil {
		t.Fatalf("SetByID: %v", err)
	}

	got, _ := json.Marshal(written["Referente"])
	if want := `{"select":{"name":"Mirko Spinato"}}`; string(got) != want {
		t.Errorf("Referente = %s, want %s", got, want)
	}
}

func TestResolveAssigneeMe(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()
	ctx := context.Background()

	t.Run("uses the profile identity", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("Marco Arnulfo"))
		f, err := s.resolveAssignee(ctx, tracker.Fields{Assignee: "me"})
		if err != nil {
			t.Fatalf("resolveAssignee: %v", err)
		}
		if f.Assignee != "Marco Arnulfo" {
			t.Errorf("Assignee = %q, want %q", f.Assignee, "Marco Arnulfo")
		}
	})

	t.Run("a partial identity resolves too", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("mirko"))
		f, err := s.resolveAssignee(ctx, tracker.Fields{Assignee: "me"})
		if err != nil {
			t.Fatalf("resolveAssignee: %v", err)
		}
		if f.Assignee != "Mirko Spinato" {
			t.Errorf("Assignee = %q, want %q", f.Assignee, "Mirko Spinato")
		}
	})

	t.Run("no identity configured", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile(""))
		_, err := s.resolveAssignee(ctx, tracker.Fields{Assignee: "me"})
		if !errors.Is(err, ErrNoIdentity) {
			t.Fatalf("error = %v, want ErrNoIdentity", err)
		}
	})
}

func TestResolveAssigneeEdges(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()
	ctx := context.Background()

	t.Run("an absent assignee is left alone", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile(""))
		if _, err := s.resolveAssignee(ctx, tracker.Fields{Status: "Fatto"}); err != nil {
			t.Fatalf("an absent assignee must not fail: %v", err)
		}
	})

	t.Run("an unknown name fails with the allowed values", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile(""))
		_, err := s.resolveAssignee(ctx, tracker.Fields{Assignee: "Marko"})
		var invalid *tracker.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %v, want *tracker.ValidationError", err)
		}
	})

	t.Run("unmapped role with a value", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()) // no Assignee
		_, err := s.resolveAssignee(ctx, tracker.Fields{Assignee: "mirko"})
		if err == nil {
			t.Fatal("resolveAssignee = nil error, want a failure naming --assignee-prop")
		}
	})
}

func TestUpsertResolvesPriority(t *testing.T) {
	var written map[string]any
	srv := capturingRoutes(t, "", &written)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), priorityProfile())
	_, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231", Priority: "alta"}, nil)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, _ := json.Marshal(written["Urgenza"])
	if want := `{"select":{"name":"ALTA"}}`; string(got) != want {
		t.Errorf("Urgenza = %s, want %s", got, want)
	}
}

func TestSetByIDResolvesPriority(t *testing.T) {
	// The third write path, the one that shares no code with Set.
	var written map[string]any
	srv := capturingRoutes(t, rowJSON, &written)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), priorityProfile())
	_, err := s.SetByID(context.Background(), testPageID, tracker.Fields{Priority: "media"}, nil)
	if err != nil {
		t.Fatalf("SetByID: %v", err)
	}

	got, _ := json.Marshal(written["Urgenza"])
	if want := `{"select":{"name":"MEDIA"}}`; string(got) != want {
		t.Errorf("Urgenza = %s, want %s", got, want)
	}
}

func TestResolvePriorityEdges(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()
	ctx := context.Background()

	t.Run("an absent priority is left alone", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), priorityProfile())
		if _, err := s.resolvePriority(ctx, tracker.Fields{Status: "Fatto"}); err != nil {
			t.Fatalf("an absent priority must not fail: %v", err)
		}
	})

	t.Run("an unknown value fails with the allowed ones", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), priorityProfile())
		_, err := s.resolvePriority(ctx, tracker.Fields{Priority: "URGENTISSIMA"})
		var invalid *tracker.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %v, want *tracker.ValidationError", err)
		}
	})

	t.Run("unmapped role with a value", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("")) // no Priority
		if _, err := s.resolvePriority(ctx, tracker.Fields{Priority: "ALTA"}); err == nil {
			t.Fatal("resolvePriority = nil error, want a failure naming --priority-prop")
		}
	})
}
