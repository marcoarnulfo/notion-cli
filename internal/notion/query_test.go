package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const pageFixture = `{
  "id": "page1",
  "url": "https://notion.so/page1",
  "last_edited_time": "2026-07-20T10:00:00.000Z",
  "properties": {
    "Name":   {"type":"title","title":[{"plain_text":"Hardening"}]},
    "Ticket": {"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
    "Stato":  {"type":"status","status":{"name":"In corso"}},
    "Scadenza":{"type":"date","date":{"start":"2026-08-01"}}
  }
}`

func TestQueryPagesPostsFilterToDataSourceEndpoint(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"results":[` + pageFixture + `],"has_more":false}`))
	}))
	defer srv.Close()

	filter := EqualsFilter("Ticket", "rich_text", "BDF-231")
	got, err := New("t", WithBaseURL(srv.URL)).QueryPages(context.Background(), "ds1", filter)
	if err != nil {
		t.Fatalf("QueryPages: %v", err)
	}
	if gotPath != "/v1/data_sources/ds1/query" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["filter"] == nil {
		t.Fatal("filter was not sent")
	}
	if len(got) != 1 {
		t.Fatalf("got %d pages, want 1", len(got))
	}
	p := got[0]
	if p.ID != "page1" || p.URL != "https://notion.so/page1" {
		t.Errorf("page identity = %+v", p)
	}
	if p.Properties["Ticket"].Text != "BDF-231" {
		t.Errorf("Ticket = %q", p.Properties["Ticket"].Text)
	}
	if p.Properties["Stato"].Text != "In corso" {
		t.Errorf("Stato = %q", p.Properties["Stato"].Text)
	}
	if p.Properties["Name"].Text != "Hardening" {
		t.Errorf("Name = %q", p.Properties["Name"].Text)
	}
	if p.Properties["Scadenza"].Date != "2026-08-01" {
		t.Errorf("Scadenza = %q", p.Properties["Scadenza"].Date)
	}
	if p.LastEditedTime.IsZero() {
		t.Error("last_edited_time was not parsed")
	}
}

func TestQueryPagesFollowsPagination(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		if page == 1 {
			w.Write([]byte(`{"results":[` + pageFixture + `],"has_more":true,"next_cursor":"cur"}`))
			return
		}
		w.Write([]byte(`{"results":[` + pageFixture + `],"has_more":false}`))
	}))
	defer srv.Close()

	got, err := New("t", WithBaseURL(srv.URL)).QueryPages(context.Background(), "ds1", nil)
	if err != nil {
		t.Fatalf("QueryPages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d pages, want 2", len(got))
	}
}

func TestEqualsFilterShapesPerPropertyType(t *testing.T) {
	rich := EqualsFilter("Ticket", "rich_text", "BDF-231")
	if rich["property"] != "Ticket" {
		t.Errorf("property = %v", rich["property"])
	}
	if rich["rich_text"].(map[string]string)["equals"] != "BDF-231" {
		t.Errorf("rich_text filter = %v", rich["rich_text"])
	}

	title := EqualsFilter("Name", "title", "X")
	if title["title"].(map[string]string)["equals"] != "X" {
		t.Errorf("title filter = %v", title["title"])
	}
}

// Mirrors TestListDataSourcesStopsOnAStalledCursor: without this, a server
// repeating one cursor would loop forever, appending the same rows each pass.
func TestQueryPagesStopsOnAStalledCursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[` + pageFixture + `],"has_more":true,"next_cursor":"same"}`))
	}))
	defer srv.Close()

	_, err := New("t", WithBaseURL(srv.URL)).QueryPages(context.Background(), "ds1", nil)
	if err == nil {
		t.Fatal("expected an error instead of an endless loop")
	}
	if !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("error does not explain the stall: %v", err)
	}
}

func TestIsEmptyFilter(t *testing.T) {
	got := IsEmptyFilter("Referente", "select")
	want := Filter{"property": "Referente", "select": map[string]bool{"is_empty": true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IsEmptyFilter = %#v, want %#v", got, want)
	}
}

func TestAndFilter(t *testing.T) {
	status := EqualsFilter("Stato", "status", "Da fare")
	assignee := EqualsFilter("Referente", "select", "Mirko Spinato")

	t.Run("no filters at all", func(t *testing.T) {
		if got := AndFilter(); got != nil {
			t.Errorf("AndFilter() = %#v, want nil so QueryPages returns every row", got)
		}
	})

	t.Run("one filter is passed through unwrapped", func(t *testing.T) {
		// Wrapping a lone filter in {"and": [...]} would work, but it changes
		// the request every existing caller sends for no gain.
		if got := AndFilter(status); !reflect.DeepEqual(got, status) {
			t.Errorf("AndFilter(one) = %#v, want the filter itself", got)
		}
	})

	t.Run("two filters compound", func(t *testing.T) {
		got := AndFilter(status, assignee)
		clauses, ok := got["and"].([]Filter)
		if !ok {
			t.Fatalf("AndFilter(two)[\"and\"] = %#v, want []Filter", got["and"])
		}
		// The clauses themselves, in order: a count alone would pass against an
		// implementation that appended the same filter twice.
		if want := []Filter{status, assignee}; !reflect.DeepEqual(clauses, want) {
			t.Errorf("clauses = %#v, want %#v", clauses, want)
		}
	})

	t.Run("nil filters are skipped", func(t *testing.T) {
		if got := AndFilter(nil, status, nil); !reflect.DeepEqual(got, status) {
			t.Errorf("AndFilter(nil, one, nil) = %#v, want the filter itself", got)
		}
	})
}

const uniqueIDPageFixture = `{
  "id": "page1",
  "url": "https://notion.so/page1",
  "last_edited_time": "2026-07-20T10:00:00.000Z",
  "properties": {
    "ID":  {"type":"unique_id","unique_id":{"prefix":"BDF","number":271}},
    "Seq": {"type":"unique_id","unique_id":{"prefix":null,"number":8}}
  }
}`

func TestQueryPagesReadsUniqueIDInTheFormThePersonSees(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[` + uniqueIDPageFixture + `],"has_more":false}`))
	}))
	defer srv.Close()

	pages, err := New("t", WithBaseURL(srv.URL)).QueryPages(context.Background(), "ds1", nil)
	if err != nil {
		t.Fatalf("QueryPages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(pages))
	}
	// The exact string matters: this is what --json prints and what a person
	// reads off the board. A test that only checked "not empty" would pass on
	// "271", on "BDF271", and on the raw JSON.
	if got := pages[0].Properties["ID"].Text; got != "BDF-271" {
		t.Errorf("ID = %q, want %q", got, "BDF-271")
	}
	// Without a prefix there is no separator to invent: the number alone.
	if got := pages[0].Properties["Seq"].Text; got != "8" {
		t.Errorf("Seq = %q, want %q", got, "8")
	}
	if got := pages[0].Properties["ID"].Type; got != "unique_id" {
		t.Errorf("ID.Type = %q, want %q", got, "unique_id")
	}
}

func TestUniqueIDEqualsFilterCarriesANumber(t *testing.T) {
	got := UniqueIDEqualsFilter("ID", 271)
	want := Filter{"property": "ID", "unique_id": map[string]int64{"equals": 271}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UniqueIDEqualsFilter = %#v, want %#v", got, want)
	}
	// The whole reason this is a separate constructor is the wire format:
	// Notion rejects a quoted value here. DeepEqual above would still pass if
	// someone switched int64 to string in both the code and the want, so the
	// marshalled form is what actually pins the contract.
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if s := string(b); !strings.Contains(s, `"equals":271`) {
		t.Errorf("marshalled to %s, want an unquoted 271", s)
	}
}
