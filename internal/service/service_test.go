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
    "Stato":{"name":"Stato","type":"status","status":{"options":[{"name":"In corso"},{"name":"Fatto"}]}}
  }}`

const rowJSON = `{
  "id":"page1","url":"https://notion.so/page1","last_edited_time":"2026-07-20T10:00:00.000Z",
  "properties":{
    "Name":{"type":"title","title":[{"plain_text":"Hardening"}]},
    "Ticket":{"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
    "Stato":{"type":"status","status":{"name":"In corso"}}
  }}`

func testProfile() config.Profile {
	return config.Profile{
		DatabaseID:   "db1",
		DataSourceID: "ds1",
		StatusType:   "status",
		Properties:   config.Properties{Ticket: "Ticket", Status: "Stato", Title: "Name"},
	}
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
	got, err := s.List(context.Background(), "In corso")
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
