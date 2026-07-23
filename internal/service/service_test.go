package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

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

func TestUpsertCreatesWhenNoRowMatches(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	got, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231", Status: "Fatto"})
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
	got, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231", Status: "Fatto"})
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
		if _, err := s.Upsert(context.Background(), f); err != nil {
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
	_, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231"})
	var dup *tracker.DuplicateError
	if !errors.As(err, &dup) {
		t.Fatalf("got %v, want *tracker.DuplicateError", err)
	}
}

func TestSetFailsWhenTheRowDoesNotExist(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.Set(context.Background(), tracker.Fields{Ticket: "BDF-999", Status: "Fatto"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if contains(seen, "POST /v1/pages") {
		t.Fatal("set created a row; only upsert may do that")
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
