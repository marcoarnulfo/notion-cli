package notion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const schemaFixture = `{
  "id": "ds1",
  "title": [{"plain_text": "Tasks"}],
  "properties": {
    "Name":   {"id":"title", "name":"Name",   "type":"title",     "title":{}},
    "Ticket": {"id":"abc",   "name":"Ticket", "type":"rich_text", "rich_text":{}},
    "Stato":  {"id":"def",   "name":"Stato",  "type":"status",
               "status":{"options":[{"name":"Backlog"},{"name":"In corso"},{"name":"Fatto"}]}},
    "Prio":   {"id":"ghi",   "name":"Prio",   "type":"select",
               "select":{"options":[{"name":"Low"},{"name":"High"}]}},
    "Scadenza":{"id":"jkl",  "name":"Scadenza","type":"date",     "date":{}}
  }
}`

func TestGetSchemaReadsPropertiesAndOptions(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(schemaFixture))
	}))
	defer srv.Close()

	got, err := New("t", WithBaseURL(srv.URL)).GetSchema(context.Background(), "ds1")
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	// The schema lives on the data source, not the database, since 2025-09-03.
	if gotPath != "/v1/data_sources/ds1" {
		t.Fatalf("path = %q", gotPath)
	}
	if got.Title != "Tasks" {
		t.Errorf("title = %q", got.Title)
	}
	if len(got.Properties) != 5 {
		t.Fatalf("got %d properties, want 5", len(got.Properties))
	}
	if p := got.Properties["Stato"]; p.Type != "status" || len(p.Options) != 3 || p.Options[2] != "Fatto" {
		t.Errorf("Stato = %+v", p)
	}
	if p := got.Properties["Prio"]; p.Type != "select" || len(p.Options) != 2 {
		t.Errorf("Prio = %+v", p)
	}
	if p := got.Properties["Scadenza"]; p.Type != "date" || len(p.Options) != 0 {
		t.Errorf("Scadenza = %+v", p)
	}
}

func TestGetSchemaEscapesTheDataSourceID(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Write([]byte(schemaFixture))
	}))
	defer srv.Close()

	// An unescaped slash would retarget the request at another endpoint
	// entirely rather than producing a readable error.
	_, err := New("t", WithBaseURL(srv.URL)).GetSchema(context.Background(), "ds/../pages")
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	if gotPath != "/v1/data_sources/ds%2F..%2Fpages" {
		t.Fatalf("path = %q, want the id escaped", gotPath)
	}
}

func TestGetSchemaHandlesAResponseWithoutProperties(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"ds1","title":[{"plain_text":"Empty"}]}`))
	}))
	defer srv.Close()

	got, err := New("t", WithBaseURL(srv.URL)).GetSchema(context.Background(), "ds1")
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	if len(got.Properties) != 0 {
		t.Fatalf("got %d properties, want none", len(got.Properties))
	}
}
