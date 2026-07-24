package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/service"
)

// The adapter copies six fields by hand from a shape with named columns into
// one with named roles. Nothing else would catch two of them being swapped —
// the same class of mistake toPageJSON's value test exists to catch.

const browseSchemaJSON = `{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{
	"Name":{"name":"Name","type":"title","title":{}},
	"Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
	"Scadenza":{"name":"Scadenza","type":"date","date":{}},
	"Stato":{"name":"Stato","type":"status","status":{"options":[{"name":"Da fare"},{"name":"Fatto"}]}}}}`

// A realistic page id: Notion always returns a UUID, and SetByID normalises
// and validates it, so a made-up "page1" would fail for the wrong reason.
const browsePageID = "11111111-2222-3333-4444-555555555555"

const browseRowJSON = `{"id":"` + browsePageID + `","url":"https://notion.so/page1",
	"parent":{"type":"data_source_id","data_source_id":"ds1"},
	"last_edited_time":"2026-07-20T10:00:00.000Z","properties":{
	"Name":{"type":"title","title":[{"plain_text":"Hardening"}]},
	"Ticket":{"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
	"Scadenza":{"type":"date","date":{"start":"2026-07-30"}},
	"Stato":{"type":"status","status":{"name":"Da fare"}}}}`

func browseTestProfile() config.Profile {
	return config.Profile{
		DataSourceID: "ds1",
		StatusType:   "status",
		Properties: config.Properties{
			Ticket: "Ticket", Status: "Stato", Title: "Name", Due: "Scadenza",
		},
	}
}

func browseTestAdapter(t *testing.T, handler http.HandlerFunc) boardAdapter {
	t.Helper()
	return browseTestAdapterFor(t, handler, browseTestProfile())
}

func browseTestAdapterFor(t *testing.T, handler http.HandlerFunc, profile config.Profile) boardAdapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client := notion.New("ntn_test", notion.WithBaseURL(srv.URL))
	return boardAdapter{ctx: context.Background(), svc: service.New(client, profile)}
}

func TestTheBoardAdapterFlattensARowOntoTheRightFields(t *testing.T) {
	b := browseTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(browseSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + browseRowJSON + `],"has_more":false}`))
	})

	rows, err := b.List("")
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 1 {
		t.Fatalf("%d rows, want 1", len(rows))
	}
	got := rows[0]
	want := map[string]string{
		"PageID": browsePageID,
		"Ticket": "BDF-231",
		"Title":  "Hardening",
		"Status": "Da fare",
		// The due date lives in the value's Date field, not its Text one.
		"Due": "2026-07-30",
		"URL": "https://notion.so/page1",
	}
	for field, wantValue := range map[string]string{
		"PageID": got.PageID, "Ticket": got.Ticket, "Title": got.Title,
		"Status": got.Status, "Due": got.Due, "URL": got.URL,
	} {
		if wantValue != want[field] {
			t.Errorf("%s = %q, want %q", field, wantValue, want[field])
		}
	}
}

// A profile that leaves the optional due role unmapped must read as an empty
// date, not as a lookup of the "" column.
func TestTheBoardAdapterToleratesAnUnmappedDueRole(t *testing.T) {
	profile := browseTestProfile()
	profile.Properties.Due = ""
	b := browseTestAdapterFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(browseSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + browseRowJSON + `],"has_more":false}`))
	}, profile)

	rows, err := b.List("")
	if err != nil {
		t.Fatal(err)
	}

	if rows[0].Due != "" {
		t.Errorf("due = %q, want empty", rows[0].Due)
	}
	// The rest of the row still has to come through: an unmapped optional role
	// must not take the mapped ones down with it.
	if rows[0].Title != "Hardening" {
		t.Errorf("title = %q", rows[0].Title)
	}
}

func TestTheBoardAdapterReadsTheStatusValuesFromTheSchema(t *testing.T) {
	b := browseTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(browseSchemaJSON))
	})

	got, err := b.Statuses()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(got, ",") != "Da fare,Fatto" {
		t.Errorf("statuses = %v", got)
	}
}

// A status column renamed in Notion since init ran must produce an actionable
// message, not an empty picker that silently offers nothing.
func TestTheBoardAdapterExplainsAMissingStatusColumn(t *testing.T) {
	b := browseTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{
			"Name":{"name":"Name","type":"title","title":{}}}}`))
	})

	_, err := b.Statuses()

	if err == nil {
		t.Fatal("no error for a status column that does not exist")
	}
	if !strings.Contains(err.Error(), "doctor") {
		t.Errorf("error = %q, want it to point at doctor", err)
	}
}

// SetStatus must address the row by page id: going back through a ticket
// lookup would fail on a duplicate the user can plainly see on screen.
func TestSetStatusWritesToThePageItWasGiven(t *testing.T) {
	var patched string
	var body map[string]any
	b := browseTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(browseSchemaJSON))
		case r.Method == http.MethodPatch:
			patched = r.URL.Path
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &body)
			w.Write([]byte(browseRowJSON))
		default:
			w.Write([]byte(browseRowJSON))
		}
	})

	if err := b.SetStatus(browsePageID, "Fatto"); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(patched, browsePageID) {
		t.Errorf("patched %q, want the page it was given", patched)
	}
	props, _ := body["properties"].(map[string]any)
	if _, ok := props["Stato"]; !ok {
		t.Errorf("payload does not write the status column: %v", body)
	}
	// Only the status: the browser's status change must not blank the title.
	if _, ok := props["Name"]; ok {
		t.Errorf("payload touches the title as well: %v", body)
	}
}
