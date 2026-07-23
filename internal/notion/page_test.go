package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
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

// A gateway can time out *after* Notion has already written the row: the
// row is created server-side on every hit, but the client only ever sees
// the 502. POST /v1/pages is the client's one non-idempotent call, so
// retrying it here — as the generic 5xx retry loop would — creates a
// second row instead of recovering. CreatePage must accept the single
// failure instead of ever making a second attempt.
func TestCreatePageDoesNotRetryOnGatewayError(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"code":"gateway_error","message":"bad gateway"}`))
	}))
	defer srv.Close()

	c := New("t", WithBaseURL(srv.URL), WithSleep(func(time.Duration) {}))
	_, err := c.CreatePage(context.Background(), "ds1", map[string]any{})
	if err == nil {
		t.Fatal("expected the 502 to surface as an error")
	}
	if attempts != 1 {
		t.Fatalf("the server saw %d requests, want 1: retrying a create risks a duplicate row", attempts)
	}
}

func TestUpdatePagePatchesProperties(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
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
	// The properties must travel wrapped: sending them bare would be accepted
	// by this test but rejected by Notion.
	props, ok := gotBody["properties"].(map[string]any)
	if !ok {
		t.Fatalf("body is not wrapped in \"properties\": %v", gotBody)
	}
	if _, ok := props["Stato"]; !ok {
		t.Fatalf("properties do not carry Stato: %v", props)
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
