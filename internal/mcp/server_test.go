package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeTracker records the calls the tools make, so a test asserts on what was
// asked of the service layer rather than on a mocked response.
type fakeTracker struct {
	row  Row
	rows []Row
	err  error

	upserted []Fields
	set      []Fields
	got      []string
	listed   []ListFilter
}

func (f *fakeTracker) Upsert(_ context.Context, fields Fields) (Row, string, error) {
	f.upserted = append(f.upserted, fields)
	if f.err != nil {
		return Row{}, "", f.err
	}
	return f.row, "created", nil
}

func (f *fakeTracker) Set(_ context.Context, fields Fields) (Row, string, error) {
	f.set = append(f.set, fields)
	if f.err != nil {
		return Row{}, "", f.err
	}
	return f.row, "updated", nil
}

func (f *fakeTracker) Get(_ context.Context, ticket string) (Row, error) {
	f.got = append(f.got, ticket)
	if f.err != nil {
		return Row{}, f.err
	}
	return f.row, nil
}

func (f *fakeTracker) List(_ context.Context, filter ListFilter) ([]Row, error) {
	f.listed = append(f.listed, filter)
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func testRow() Row {
	return Row{
		Ticket: "BDF-231", Title: "Hardening", Status: "Fatto",
		PageID: "page1", URL: "https://notion.so/page1",
		LastEditedTime: "2026-07-20T10:00:00Z",
		Assignee:       "Mirko Spinato",
		Priority:       "ALTA",
	}
}

// connect runs the real server over an in-memory transport and returns a
// connected client session: the protocol is exercised end to end, only the
// service layer is faked.
func connect(t *testing.T, tracker Tracker) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	server := NewServer(tracker, "test")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Run(ctx, serverTransport)
	}()

	session, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "test"}, nil).
		Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		session.Close()
		<-done
	})
	return session
}

// call invokes a tool and returns its structured output, decoded.
func call(t *testing.T, s *sdk.ClientSession, name string, args map[string]any, into any) *sdk.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s reported an error: %v", name, res.Content)
	}
	if into != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("%s output: %v", name, err)
		}
	}
	return res
}

// The tool names and descriptions are the whole interface an agent sees. A
// rename is a breaking change for every agent configured against it.
func TestTheServerExposesTheFourTools(t *testing.T) {
	session := connect(t, &fakeTracker{})

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("%s has no description for an agent to read", tool.Name)
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "get_task,list_tasks,set_task,upsert_task" {
		t.Fatalf("tools = %v", names)
	}
}

func TestUpsertToolPassesItsArgumentsThrough(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	var out writeResult
	call(t, session, "upsert_task", map[string]any{
		"ticket": "BDF-231", "title": "Hardening", "status": "Fatto", "due": "2026-08-01",
	}, &out)

	if len(tracker.upserted) != 1 {
		t.Fatalf("upsert called %d times", len(tracker.upserted))
	}
	got := tracker.upserted[0]
	if got.Ticket != "BDF-231" || got.Title != "Hardening" ||
		got.Status != "Fatto" || got.Due != "2026-08-01" {
		t.Errorf("fields = %+v", got)
	}
	if out.Action != "created" || out.Row.PageID != "page1" {
		t.Errorf("result = %+v", out)
	}
}

// Omitted fields must arrive empty, which is what "leave this alone" means
// everywhere else in notion-track.
func TestOmittedArgumentsArriveEmpty(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	call(t, session, "upsert_task", map[string]any{"ticket": "BDF-231"}, nil)

	got := tracker.upserted[0]
	if got.Title != "" || got.Status != "" || got.Due != "" {
		t.Errorf("fields = %+v, want only the ticket set", got)
	}
}

func TestSetTool(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	var out writeResult
	call(t, session, "set_task", map[string]any{"ticket": "BDF-231", "status": "Fatto"}, &out)

	if len(tracker.set) != 1 || tracker.set[0].Ticket != "BDF-231" {
		t.Fatalf("set calls = %+v", tracker.set)
	}
	if len(tracker.upserted) != 0 {
		t.Error("set_task reached upsert")
	}
	if out.Action != "updated" {
		t.Errorf("action = %q", out.Action)
	}
}

func TestGetTool(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	var out Row
	call(t, session, "get_task", map[string]any{"ticket": "BDF-231"}, &out)

	if len(tracker.got) != 1 || tracker.got[0] != "BDF-231" {
		t.Fatalf("get calls = %v", tracker.got)
	}
	// The keys are the CLI's documented --json contract; an agent that read
	// the README finds the same ones here.
	if out.Ticket != "BDF-231" || out.Title != "Hardening" || out.Status != "Fatto" ||
		out.URL != "https://notion.so/page1" || out.LastEditedTime == "" {
		t.Errorf("row = %+v", out)
	}
}

func TestListToolPassesTheStatusFilter(t *testing.T) {
	tracker := &fakeTracker{rows: []Row{testRow(), testRow()}}
	session := connect(t, tracker)

	var out listResult
	call(t, session, "list_tasks", map[string]any{"status": "Fatto"}, &out)

	if len(tracker.listed) != 1 || tracker.listed[0].Status != "Fatto" {
		t.Fatalf("list calls = %v", tracker.listed)
	}
	if len(out.Rows) != 2 {
		t.Errorf("%d rows returned", len(out.Rows))
	}
}

func TestListToolWithNoFilterAsksForEverything(t *testing.T) {
	tracker := &fakeTracker{}
	session := connect(t, tracker)

	call(t, session, "list_tasks", map[string]any{}, nil)

	if len(tracker.listed) != 1 || tracker.listed[0] != (ListFilter{}) {
		t.Fatalf("list calls = %v, want one for every row", tracker.listed)
	}
}

// A failure from the service layer must reach the agent as a tool error it can
// read and react to, not as a silent empty result.
func TestAServiceFailureIsReportedToTheAgent(t *testing.T) {
	tracker := &fakeTracker{err: errors.New("ticket key matches more than one row")}
	session := connect(t, tracker)

	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "get_task",
		Arguments: map[string]any{"ticket": "BDF-231"},
	})
	if err != nil {
		t.Fatalf("the call itself failed: %v", err)
	}

	if !res.IsError {
		t.Fatal("a service failure was reported as success")
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "more than one row") {
		t.Errorf("content = %q, want the reason", text)
	}
}

func TestUpsertToolCarriesTheAssignee(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	call(t, session, "upsert_task", map[string]any{
		"ticket": "BDF-231", "assignee": "mirko",
	}, nil)

	if len(tracker.upserted) != 1 {
		t.Fatalf("upsert calls = %d, want 1", len(tracker.upserted))
	}
	if got := tracker.upserted[0].Assignee; got != "mirko" {
		t.Errorf("Assignee = %q, want %q", got, "mirko")
	}
}

func TestSetToolCarriesUnassign(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	call(t, session, "set_task", map[string]any{
		"ticket": "BDF-231", "unassign": true,
	}, nil)

	if len(tracker.set) != 1 {
		t.Fatalf("set calls = %d, want 1", len(tracker.set))
	}
	if !tracker.set[0].Unassign {
		t.Error("Unassign = false, want true")
	}
}

func TestListToolFiltersByAssignee(t *testing.T) {
	tracker := &fakeTracker{rows: []Row{testRow()}}
	session := connect(t, tracker)

	call(t, session, "list_tasks", map[string]any{"assignee": "me"}, nil)

	if len(tracker.listed) != 1 || tracker.listed[0].Assignee != "me" {
		t.Fatalf("list calls = %v, want one filtered by me", tracker.listed)
	}
}

func TestListToolFiltersByUnassigned(t *testing.T) {
	tracker := &fakeTracker{rows: []Row{testRow()}}
	session := connect(t, tracker)

	call(t, session, "list_tasks", map[string]any{"unassigned": true}, nil)

	if len(tracker.listed) != 1 || !tracker.listed[0].Unassigned {
		t.Fatalf("list calls = %v, want one filtered by unassigned", tracker.listed)
	}
}

func TestGetToolExposesTheAssignee(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	var row Row
	call(t, session, "get_task", map[string]any{"ticket": "BDF-231"}, &row)

	if row.Assignee != "Mirko Spinato" {
		t.Errorf("Assignee = %q, want %q", row.Assignee, "Mirko Spinato")
	}
}

func TestUpsertToolCarriesThePriority(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	call(t, session, "upsert_task", map[string]any{
		"ticket": "BDF-231", "priority": "alta",
	}, nil)

	if len(tracker.upserted) != 1 || tracker.upserted[0].Priority != "alta" {
		t.Fatalf("upsert calls = %v, want one carrying the priority", tracker.upserted)
	}
}

func TestListToolFiltersByPriority(t *testing.T) {
	tracker := &fakeTracker{rows: []Row{testRow()}}
	session := connect(t, tracker)

	call(t, session, "list_tasks", map[string]any{"priority": "ALTA"}, nil)

	if len(tracker.listed) != 1 || tracker.listed[0].Priority != "ALTA" {
		t.Fatalf("list calls = %v, want one filtered by priority", tracker.listed)
	}
}

func TestGetToolExposesThePriority(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	var row Row
	call(t, session, "get_task", map[string]any{"ticket": "BDF-231"}, &row)

	if row.Priority != "ALTA" {
		t.Errorf("Priority = %q, want %q", row.Priority, "ALTA")
	}
}
