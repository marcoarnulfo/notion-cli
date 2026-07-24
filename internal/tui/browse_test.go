package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeBoard stands in for the service layer and records what the UI asked of
// it, so a test can assert on the call rather than on a mocked response.
type fakeBoard struct {
	rows     []Row
	statuses []string

	listErr   error
	setErr    error
	createErr error

	listedFilters []string
	setCalls      []string // "pageID→status"
	created       []string // "ticket|title|status"
}

func (b *fakeBoard) List(status string) ([]Row, error) {
	b.listedFilters = append(b.listedFilters, status)
	if b.listErr != nil {
		return nil, b.listErr
	}
	if status == "" {
		return b.rows, nil
	}
	var out []Row
	for _, r := range b.rows {
		if r.Status == status {
			out = append(out, r)
		}
	}
	return out, nil
}

func (b *fakeBoard) SetStatus(pageID, status string) error {
	b.setCalls = append(b.setCalls, pageID+"→"+status)
	return b.setErr
}

func (b *fakeBoard) Create(ticket, title, status string) error {
	b.created = append(b.created, strings.Join([]string{ticket, title, status}, "|"))
	return b.createErr
}

func (b *fakeBoard) Statuses() ([]string, error) { return b.statuses, nil }

func testBoard() *fakeBoard {
	return &fakeBoard{
		rows: []Row{
			{PageID: "p1", Ticket: "BDF-1", Title: "Hardening", Status: "In corso",
				Due: "2026-07-30", URL: "https://notion.so/p1"},
			{PageID: "p2", Ticket: "BDF-2", Title: "Grafici", Status: "Da fare",
				URL: "https://notion.so/p2"},
		},
		statuses: []string{"Da fare", "In corso", "Fatto"},
	}
}

func pressBrowse(t *testing.T, m BrowseModel, keys ...string) (BrowseModel, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.Update(keyMsg(k))
		var ok bool
		m, ok = next.(BrowseModel)
		if !ok {
			t.Fatalf("Update returned %T, not a BrowseModel", next)
		}
	}
	return m, cmd
}

// runCmd executes a command the way the runtime does: feed its message back
// in, then follow whatever command that message produces in turn. A write that
// asks for a reload is two links of that chain, and stopping at the first
// would test half the behaviour.
func runCmd(t *testing.T, m BrowseModel, cmd tea.Cmd) BrowseModel {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command, got none")
	}
	// A bounded loop, not `for cmd != nil`: a model that kept producing
	// commands would otherwise hang the suite instead of failing it.
	for i := 0; cmd != nil && i < 8; i++ {
		msg := cmd()
		if _, quitting := msg.(tea.QuitMsg); quitting {
			return m
		}
		next, nextCmd := m.Update(msg)
		out, ok := next.(BrowseModel)
		if !ok {
			t.Fatalf("Update returned %T, not a BrowseModel", next)
		}
		m, cmd = out, nextCmd
	}
	return m
}

// loaded is the browser as it looks once the first load has come back.
func loaded(t *testing.T, board Board) BrowseModel {
	t.Helper()
	m := NewBrowser(board)
	return runCmd(t, m, m.Init())
}

func TestTheBrowserLoadsTheRowsOnStart(t *testing.T) {
	board := testBoard()

	m := loaded(t, board)

	if m.stage != browseList {
		t.Fatalf("stage = %v, want the list", m.stage)
	}
	if len(m.rows.Items()) != 2 {
		t.Fatalf("%d rows on screen, want 2", len(m.rows.Items()))
	}
	view := m.View()
	for _, want := range []string{"BDF-1", "Hardening", "In corso", "2026-07-30"} {
		if !strings.Contains(view, want) {
			t.Errorf("the list does not show %q:\n%s", want, view)
		}
	}
}

// Nothing loaded means there is no screen to fall back to, so the failure has
// to be shown rather than swallowed into a footer nobody can act on.
func TestAFirstLoadFailureIsTerminal(t *testing.T) {
	board := testBoard()
	board.listErr = errors.New("token rejected")

	m := loaded(t, board)

	if m.stage != browseFatal {
		t.Fatalf("stage = %v, want the error screen", m.stage)
	}
	if !strings.Contains(m.View(), "token rejected") {
		t.Errorf("the error screen hides the reason:\n%s", m.View())
	}
}

// A refresh that fails must not throw away rows the user can still read.
func TestAFailedReloadKeepsTheRowsOnScreen(t *testing.T) {
	board := testBoard()
	m := loaded(t, board)

	board.listErr = errors.New("network is down")
	m, cmd := pressBrowse(t, m, "r")
	m = runCmd(t, m, cmd)

	if m.stage != browseList {
		t.Fatalf("stage = %v, want to stay on the list", m.stage)
	}
	if len(m.rows.Items()) != 2 {
		t.Errorf("%d rows left, want the stale two", len(m.rows.Items()))
	}
	if !strings.Contains(m.note, "network is down") {
		t.Errorf("note = %q, want it to mention the failure", m.note)
	}
}

func TestEnterOpensTheDetailAndEscapeReturns(t *testing.T) {
	m := loaded(t, testBoard())

	m, _ = pressBrowse(t, m, "enter")
	if m.stage != browseDetail {
		t.Fatalf("stage = %v, want the detail", m.stage)
	}
	view := m.View()
	for _, want := range []string{"BDF-1", "Hardening", "In corso", "https://notion.so/p1"} {
		if !strings.Contains(view, want) {
			t.Errorf("the detail does not show %q:\n%s", want, view)
		}
	}

	m, _ = pressBrowse(t, m, "esc")
	if m.stage != browseList {
		t.Errorf("stage = %v, want back at the list", m.stage)
	}
}

func TestChangingAStatusWritesTheSelectedRowAndReloads(t *testing.T) {
	board := testBoard()
	m := loaded(t, board)

	// Move to the second row, open the status picker, pick "Fatto" (third).
	m, _ = pressBrowse(t, m, "down", "s")
	if m.stage != browsePickStatus {
		t.Fatalf("stage = %v, want the status picker", m.stage)
	}
	_, cmd := pressBrowse(t, m, "down", "down", "enter")
	runCmd(t, m, cmd)

	if len(board.setCalls) != 1 || board.setCalls[0] != "p2→Fatto" {
		t.Fatalf("set calls = %v, want one for p2→Fatto", board.setCalls)
	}
	// The row on screen no longer matches Notion, so the write asks for a
	// refresh rather than leaving a stale status in place.
	if len(board.listedFilters) != 2 {
		t.Errorf("list called %d times, want a reload after the write", len(board.listedFilters))
	}
}

// The picker only offers values the schema accepts, which is what stops the
// TUI from writing a status the board would reject.
func TestTheStatusPickerOffersOnlyTheSchemaValues(t *testing.T) {
	m := loaded(t, testBoard())

	m, _ = pressBrowse(t, m, "s")

	var got []string
	for _, item := range m.statuses.Items() {
		got = append(got, item.(statusItem).Title())
	}
	if strings.Join(got, ",") != "Da fare,In corso,Fatto" {
		t.Errorf("picker offers %v", got)
	}
}

func TestFilteringByStatusReloadsWithThatFilter(t *testing.T) {
	board := testBoard()
	m := loaded(t, board)

	m, _ = pressBrowse(t, m, "f")
	if m.stage != browsePickFilter {
		t.Fatalf("stage = %v, want the filter picker", m.stage)
	}
	// The filter picker leads with "— all —", so "Da fare" is the second entry.
	m, cmd := pressBrowse(t, m, "down", "enter")
	m = runCmd(t, m, cmd)

	if m.filter != "Da fare" {
		t.Fatalf("filter = %q, want Da fare", m.filter)
	}
	if len(board.listedFilters) != 2 || board.listedFilters[1] != "Da fare" {
		t.Fatalf("list filters = %v, want a reload filtered by Da fare", board.listedFilters)
	}
	if len(m.rows.Items()) != 1 {
		t.Errorf("%d rows shown, want only the matching one", len(m.rows.Items()))
	}
	if !strings.Contains(m.View(), "filter: Da fare") {
		t.Errorf("the screen does not show the active filter:\n%s", m.View())
	}
}

// Clearing the filter has to be reachable from the same picker that set it.
func TestTheFilterCanBeCleared(t *testing.T) {
	board := testBoard()
	m := loaded(t, board)

	m, cmd := pressBrowse(t, m, "f", "down", "enter")
	m = runCmd(t, m, cmd)
	m, cmd = pressBrowse(t, m, "f", "enter") // "— all —" is the first entry
	m = runCmd(t, m, cmd)

	if m.filter != "" {
		t.Fatalf("filter = %q, want it cleared", m.filter)
	}
	if len(m.rows.Items()) != 2 {
		t.Errorf("%d rows shown, want all of them back", len(m.rows.Items()))
	}
}

func TestCreatingATaskNeedsATicketKey(t *testing.T) {
	board := testBoard()
	m := loaded(t, board)

	m, _ = pressBrowse(t, m, "n")
	if m.stage != browseCreate {
		t.Fatalf("stage = %v, want the create form", m.stage)
	}

	m, cmd := pressBrowse(t, m, "enter")

	if cmd != nil || len(board.created) != 0 {
		t.Fatal("a row was created with no ticket key")
	}
	if m.stage != browseCreate {
		t.Errorf("stage = %v, want to stay on the form", m.stage)
	}
	if !strings.Contains(m.View(), "ticket key is required") {
		t.Errorf("the form does not say what is missing:\n%s", m.View())
	}
}

func TestCreatingATask(t *testing.T) {
	board := testBoard()
	m := loaded(t, board)

	m, _ = pressBrowse(t, m, "n", "B", "D", "F", "-", "9")
	m, _ = pressBrowse(t, m, "tab")
	m, cmd := pressBrowse(t, m, "N", "e", "w", "enter")
	m = runCmd(t, m, cmd)

	if len(board.created) != 1 || board.created[0] != "BDF-9|New|" {
		t.Fatalf("created = %v, want one BDF-9 titled New with no status", board.created)
	}
	if m.stage != browseList {
		t.Errorf("stage = %v, want back at the list", m.stage)
	}
	if len(board.listedFilters) != 2 {
		t.Errorf("list called %d times, want a reload after the create", len(board.listedFilters))
	}
}

// Creating from a filtered view lands the row in the view being looked at,
// which is nearly always what was meant.
func TestATaskCreatedUnderAFilterTakesThatStatus(t *testing.T) {
	board := testBoard()
	m := loaded(t, board)

	m, cmd := pressBrowse(t, m, "f", "down", "enter")
	m = runCmd(t, m, cmd)
	m, _ = pressBrowse(t, m, "n", "X")
	_, cmd = pressBrowse(t, m, "enter")
	runCmd(t, m, cmd)

	if len(board.created) != 1 || board.created[0] != "X||Da fare" {
		t.Fatalf("created = %v, want the active filter as the status", board.created)
	}
}

func TestOpeningARowHandsTheURLToTheBrowser(t *testing.T) {
	var opened string
	m := loaded(t, testBoard())
	m.openInBrowser = func(url string) error { opened = url; return nil }

	m, cmd := pressBrowse(t, m, "o")
	m = runCmd(t, m, cmd)

	if opened != "https://notion.so/p1" {
		t.Fatalf("opened %q, want the selected row's URL", opened)
	}
	if !strings.Contains(m.note, "opened") {
		t.Errorf("note = %q", m.note)
	}
}

func TestCopyingARowPutsTheURLOnTheClipboard(t *testing.T) {
	var copied string
	m := loaded(t, testBoard())
	m.copyToClipboard = func(s string) error { copied = s; return nil }

	m, cmd := pressBrowse(t, m, "y")
	m = runCmd(t, m, cmd)

	if copied != "https://notion.so/p1" {
		t.Fatalf("copied %q, want the selected row's URL", copied)
	}
	if !strings.Contains(m.note, "copied") {
		t.Errorf("note = %q", m.note)
	}
}

// A headless box with no clipboard tool is a normal place to run this, so the
// failure belongs in the footer, not in a torn-down UI.
func TestAnUnreachableClipboardIsReportedNotFatal(t *testing.T) {
	m := loaded(t, testBoard())
	m.copyToClipboard = func(string) error { return errors.New("no xclip") }

	m, cmd := pressBrowse(t, m, "y")
	m = runCmd(t, m, cmd)

	if m.stage != browseList {
		t.Fatalf("stage = %v, want to stay on the list", m.stage)
	}
	if !strings.Contains(m.note, "no xclip") {
		t.Errorf("note = %q, want the reason", m.note)
	}
}

func TestAnEmptyBoardSaysSo(t *testing.T) {
	board := &fakeBoard{statuses: []string{"Da fare"}}

	m := loaded(t, board)

	if !strings.Contains(m.View(), "no matching tasks") {
		t.Errorf("an empty board shows nothing at all:\n%s", m.View())
	}
}

// A board keyed by task name maps ticket and title onto one column; printing
// the identical value twice would read as a display bug, the same reasoning
// list's human output follows.
func TestARowKeyedByItsTitleIsNotShownTwice(t *testing.T) {
	board := &fakeBoard{rows: []Row{
		{PageID: "p1", Ticket: "Hardening", Title: "Hardening", Status: "Fatto"},
	}}

	m := loaded(t, board)

	if n := strings.Count(m.View(), "Hardening"); n != 1 {
		t.Errorf("the value appears %d times, want once:\n%s", n, m.View())
	}
}

func TestQuitting(t *testing.T) {
	for _, key := range []string{"q", "esc", "ctrl+c"} {
		_, cmd := pressBrowse(t, loaded(t, testBoard()), key)
		if cmd == nil {
			t.Errorf("%q did not quit", key)
		}
	}
}
