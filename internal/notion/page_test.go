package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreatePageUsesDataSourceParent(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/pages" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(pageFixture))
	}))
	defer srv.Close()

	props := map[string]any{"Ticket": map[string]any{"rich_text": []any{}}}
	got, err := New("t", WithBaseURL(srv.URL)).CreatePage(context.Background(), "ds1", props)
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	parent, _ := gotBody["parent"].(map[string]any)
	// 2025-09-03 moved the parent from database_id to data_source_id.
	if parent["type"] != "data_source_id" || parent["data_source_id"] != "ds1" {
		t.Fatalf("parent = %v", parent)
	}
	if got.ID != "page1" {
		t.Errorf("page id = %q", got.ID)
	}
}

func TestUpdatePagePatchesProperties(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Write([]byte(pageFixture))
	}))
	defer srv.Close()

	_, err := New("t", WithBaseURL(srv.URL)).UpdatePage(context.Background(), "page1",
		map[string]any{"Stato": map[string]any{"status": map[string]string{"name": "Fatto"}}})
	if err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/v1/pages/page1" {
		t.Fatalf("got %s %s", gotMethod, gotPath)
	}
}

func TestMeReturnsTheBotName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/users/me" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"id":"bot1","name":"notion-track","type":"bot"}`))
	}))
	defer srv.Close()

	name, err := New("t", WithBaseURL(srv.URL)).Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if name != "notion-track" {
		t.Fatalf("name = %q", name)
	}
}
