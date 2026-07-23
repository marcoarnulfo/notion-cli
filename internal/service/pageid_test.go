package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

// testPageID/testPageIDCanonical mirror what notion.NormalizePageID does: the
// service layer must send the canonical dashed form on the wire regardless
// of which of the three accepted shapes the caller passed in.
const (
	testPageID          = "23fb4e5c8a5f4d21b7c9d0e1f2a3b4c5"
	testPageIDCanonical = "23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5"
)

// id matches testPageIDCanonical: a real Notion response identifies the page
// that was actually requested, which is what lets tests assert SetByID
// patches the same id GetPage resolved rather than some other one.
const rowJSONWithParent = `{
  "id":"23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5","url":"https://notion.so/page1","last_edited_time":"2026-07-20T10:00:00.000Z",
  "parent":{"type":"data_source_id","data_source_id":"ds1"},
  "properties":{
    "Name":{"type":"title","title":[{"plain_text":"Hardening"}]},
    "Ticket":{"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
    "Stato":{"type":"status","status":{"name":"In corso"}}
  }}`

const rowJSONOtherDataSource = `{
  "id":"23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5","url":"https://notion.so/page1","last_edited_time":"2026-07-20T10:00:00.000Z",
  "parent":{"type":"data_source_id","data_source_id":"ds-other"},
  "properties":{}
}`

func TestGetByIDReadsThePageDirectlyWithoutQuerying(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Write([]byte(rowJSONWithParent))
	}))
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	got, err := s.GetByID(context.Background(), testPageID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != testPageIDCanonical {
		t.Fatalf("id = %q", got.ID)
	}
	want := "GET /v1/pages/" + testPageIDCanonical
	if !contains(seen, want) {
		t.Fatalf("did not read the page directly: %v", seen)
	}
	if contains(seen, "POST /v1/data_sources/ds1/query") {
		t.Fatal("GetByID must not query by key")
	}
}

func TestSetByIDUpdatesTheCorrectPageWithoutQuerying(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(schemaJSON))
			return
		}
		w.Write([]byte(rowJSONWithParent))
	}))
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	res, err := s.SetByID(context.Background(), testPageID, tracker.Fields{Status: "Fatto"})
	if err != nil {
		t.Fatalf("SetByID: %v", err)
	}
	if res.Action != "updated" {
		t.Fatalf("action = %q, want updated", res.Action)
	}
	want := "/v1/pages/" + testPageIDCanonical
	if !contains(seen, "GET "+want) {
		t.Fatalf("did not read the page directly: %v", seen)
	}
	if !contains(seen, "PATCH "+want) {
		t.Fatalf("did not patch the page directly: %v", seen)
	}
	if contains(seen, "POST /v1/data_sources/ds1/query") {
		t.Fatal("SetByID must not query by key")
	}
}

func TestGetByIDRejectsAPageFromAnotherDataSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(rowJSONOtherDataSource))
	}))
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.GetByID(context.Background(), testPageID)
	if !errors.Is(err, ErrPageOutsideProfile) {
		t.Fatalf("got %v, want ErrPageOutsideProfile", err)
	}
}

func TestSetByIDRejectsAPageFromAnotherDataSource(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Write([]byte(rowJSONOtherDataSource))
	}))
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.SetByID(context.Background(), testPageID, tracker.Fields{Status: "Fatto"})
	if !errors.Is(err, ErrPageOutsideProfile) {
		t.Fatalf("got %v, want ErrPageOutsideProfile", err)
	}
	if contains(seen, "PATCH /v1/pages/"+testPageIDCanonical) {
		t.Fatal("a page outside the profile must never be patched")
	}
}

// A malformed page id must fail before any request, exactly like an empty
// ticket does for Get/Set/Upsert.
func TestGetByIDRejectsAMalformedPageID(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Write([]byte(rowJSONWithParent))
	}))
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.GetByID(context.Background(), "not-a-page-id")
	if !errors.Is(err, notion.ErrMalformedPageID) {
		t.Fatalf("got %v, want notion.ErrMalformedPageID", err)
	}
	if len(seen) != 0 {
		t.Fatalf("a malformed page id reached the API: %v", seen)
	}
}

func TestGetByIDRejectsAnEmptyPageID(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Write([]byte(rowJSONWithParent))
	}))
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.GetByID(context.Background(), "")
	if !errors.Is(err, ErrEmptyPageID) {
		t.Fatalf("got %v, want ErrEmptyPageID", err)
	}
	if len(seen) != 0 {
		t.Fatalf("an empty page id reached the API: %v", seen)
	}
}

func TestSetByIDRejectsAnEmptyPageID(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Write([]byte(rowJSONWithParent))
	}))
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.SetByID(context.Background(), "", tracker.Fields{Status: "Fatto"})
	if !errors.Is(err, ErrEmptyPageID) {
		t.Fatalf("got %v, want ErrEmptyPageID", err)
	}
	if len(seen) != 0 {
		t.Fatalf("an empty page id reached the API: %v", seen)
	}
}

func TestGetByIDMapsA404ToErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":"object_not_found","message":"not found"}`))
	}))
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.GetByID(context.Background(), testPageID)
	if !errors.Is(err, notion.ErrNotFound) {
		t.Fatalf("got %v, want notion.ErrNotFound", err)
	}
}
