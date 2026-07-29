package cli

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/mcp"
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

// withFakeMCPServer swaps the blocking stdio server for a recorder: the real
// one reads from stdin until its peer disconnects, and a test has no peer.
func withFakeMCPServer(t *testing.T, err error) *mcp.Tracker {
	t.Helper()
	var captured mcp.Tracker
	old := runMCPServer
	runMCPServer = func(_ context.Context, tr mcp.Tracker) error {
		captured = tr
		return err
	}
	t.Cleanup(func() { runMCPServer = old })
	return &captured
}

func TestMCPCommandServesTheConfiguredProfile(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedRow)
	tracker := withFakeMCPServer(t, nil)

	if code := executeArgs([]string{"mcp", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if *tracker == nil {
		t.Fatal("the server was started with no tracker")
	}
}

// A broken setup must fail before the protocol starts: an MCP client cannot
// act on a handshake that succeeds and then errors on every call.
func TestMCPCommandFailsBeforeServingWhenUnconfigured(t *testing.T) {
	tracker := withFakeMCPServer(t, nil)

	if code := executeArgs([]string{"mcp", "--config", "/nonexistent/config.yml"}); code == ExitOK {
		t.Fatal("exit code 0 with no usable config")
	}
	if *tracker != nil {
		t.Error("the server started despite an unusable config")
	}
}

// The adapter is what an agent's rows actually come from, and it reuses
// toPageJSON — the CLI's documented --json shape — so the two cannot drift.
func TestTheMCPAdapterReturnsTheDocumentedShape(t *testing.T) {
	b := browseTestAdapterFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(browseSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + browseRowJSON + `],"has_more":false}`))
	}, browseTestProfile())
	adapter := trackerAdapter{svc: b.svc}

	rows, err := adapter.List(context.Background(), mcp.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 1 {
		t.Fatalf("%d rows", len(rows))
	}
	got := rows[0]
	if got.Ticket != "BDF-231" || got.Title != "Hardening" || got.Status != "Da fare" ||
		got.PageID != browsePageID || got.URL != "https://notion.so/page1" ||
		got.LastEditedTime == "" {
		t.Errorf("row = %+v", got)
	}
}

func TestTheMCPConversionsStayDirect(t *testing.T) {
	// A compile-time guarantee turned into a test: these conversions only
	// compile while the structs stay identical, which is what keeps the
	// documented --json contract and what an agent sees from drifting apart.
	// If this file stops compiling, that is the feature working.
	var p pageJSON
	_ = mcp.Row(p)

	var f mcp.Fields
	_ = tracker.Fields(f)

	var lf mcp.ListFilter
	_ = service.ListFilter(lf)
}

func TestMCPRowMirrorsPageJSON(t *testing.T) {
	// The two structs are converted with a direct type conversion, which only
	// compiles while they match field for field. This test states the contract
	// the compiler enforces, so a reviewer reading either file finds out why.
	pj := reflect.TypeOf(pageJSON{})
	row := reflect.TypeOf(mcp.Row{})
	if pj.NumField() != row.NumField() {
		t.Fatalf("pageJSON has %d fields, mcp.Row has %d", pj.NumField(), row.NumField())
	}
	for i := 0; i < pj.NumField(); i++ {
		a, b := pj.Field(i), row.Field(i)
		if a.Name != b.Name || a.Type != b.Type || a.Tag.Get("json") != b.Tag.Get("json") {
			t.Errorf("field %d: pageJSON has %s %s `%s`, mcp.Row has %s %s `%s`",
				i, a.Name, a.Type, a.Tag.Get("json"), b.Name, b.Type, b.Tag.Get("json"))
		}
	}
	if _, ok := pj.FieldByName("ID"); !ok {
		t.Error("pageJSON has no ID field")
	}
}
