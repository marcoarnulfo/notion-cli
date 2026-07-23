package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListDataSourcesFiltersOnDataSourceObjects(t *testing.T) {
	var gotFilter map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotFilter, _ = body["filter"].(map[string]any)
		w.Write([]byte(`{"results":[],"has_more":false}`))
	}))
	defer srv.Close()

	if _, err := New("t", WithBaseURL(srv.URL)).ListDataSources(context.Background()); err != nil {
		t.Fatalf("ListDataSources: %v", err)
	}
	// 2025-09-03 renamed this value from "database" to "data_source".
	if gotFilter["value"] != "data_source" || gotFilter["property"] != "object" {
		t.Fatalf("filter = %v, want object/data_source", gotFilter)
	}
}

func TestListDataSourcesFollowsPagination(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		if page == 1 {
			w.Write([]byte(`{"results":[
				{"object":"data_source","id":"ds1","title":[{"plain_text":"Tasks"}],
				 "parent":{"type":"database_id","database_id":"db1"}}
			],"has_more":true,"next_cursor":"cur"}`))
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["start_cursor"] != "cur" {
			t.Errorf("start_cursor = %v, want cur", body["start_cursor"])
		}
		w.Write([]byte(`{"results":[
			{"object":"data_source","id":"ds2","title":[{"plain_text":"Roadmap"}],
			 "parent":{"type":"database_id","database_id":"db2"}}
		],"has_more":false}`))
	}))
	defer srv.Close()

	got, err := New("t", WithBaseURL(srv.URL)).ListDataSources(context.Background())
	if err != nil {
		t.Fatalf("ListDataSources: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d data sources, want 2", len(got))
	}
	if got[0].ID != "ds1" || got[0].Title != "Tasks" || got[0].DatabaseID != "db1" {
		t.Errorf("first result = %+v", got[0])
	}
	if got[1].ID != "ds2" {
		t.Errorf("second result = %+v", got[1])
	}
}
