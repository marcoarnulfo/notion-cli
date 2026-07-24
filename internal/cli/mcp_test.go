package cli

import (
	"context"
	"net/http"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/mcp"
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

	rows, err := adapter.List(context.Background(), "")
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
