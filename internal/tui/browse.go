package tui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Row is one tracked row, flattened to what the browser shows.
//
// The browser never sees a notion.Page or the profile's property mapping:
// resolving "which column is the status" is the adapter's job, done once, so
// every screen here works with plain strings.
type Row struct {
	PageID string
	Ticket string
	Title  string
	Status string
	Due    string
	URL    string
}

// Board is the slice of the service layer the browsing TUI needs, declared
// here where it is consumed rather than beside the implementation. internal/cli
// supplies the adapter; a test supplies a fake and drives the whole UI without
// a network.
type Board interface {
	// List returns the rows, optionally narrowed to one status.
	List(status string) ([]Row, error)
	// SetStatus moves one row, addressed by page id because a ticket key is
	// not guaranteed unique and the row is already in hand.
	SetStatus(pageID, status string) error
	// Create adds a row. An empty status leaves it unset.
	Create(ticket, title, status string) error
	// Statuses are the values the status property accepts.
	Statuses() ([]string, error)
}

type browseStage int

const (
	browseLoading browseStage = iota
	browseList
	browseDetail
	browsePickStatus // changing the selected row's status
	browsePickFilter // narrowing the list to one status
	browseCreate
	browseFatal // could not load anything at all
)

// BrowseModel is the interactive view over the tracked rows.
type BrowseModel struct {
	board Board

	// Injected so tests can assert what would have been copied or opened, and
	// so a machine without xclip or a browser degrades to a message instead of
	// a failure.
	copyToClipboard func(string) error
	openInBrowser   func(string) error

	stage    browseStage
	rows     list.Model
	statuses list.Model
	ticket   textinput.Model
	title    textinput.Model
	focus    int // which create-form field has focus

	filter    string // active status filter, "" for all
	statusSet []string
	note      string // transient one-line message under the list
	err       error  // fatal: nothing could be loaded

	width, height int
}

// NewBrowser builds the browser over board.
func NewBrowser(board Board) BrowseModel {
	m := BrowseModel{
		board:           board,
		copyToClipboard: copyToClipboard,
		openInBrowser:   openInBrowser,
		stage:           browseLoading,
		width:           defaultWidth,
		height:          defaultHeight,
	}
	m.rows = newList(nil, "notion-track", m.width, m.listHeight())
	m.rows.SetDelegate(rowDelegate{})

	m.ticket = textinput.New()
	m.ticket.Placeholder = "ticket key"
	m.title = textinput.New()
	m.title.Placeholder = "title"
	return m
}

func (m BrowseModel) listHeight() int {
	if h := m.height - 4; h > 0 {
		return h
	}
	return 1
}

func (m BrowseModel) Init() tea.Cmd { return m.loadRows() }

// Messages for the work that happens off the main loop.
type rowsLoadedMsg struct{ rows []Row }
type rowsFailedMsg struct{ err error }

// actionDoneMsg reports a write. reload asks for a refresh, because the row on
// screen no longer matches the one in Notion.
type actionDoneMsg struct {
	note   string
	err    error
	reload bool
}

func (m BrowseModel) loadRows() tea.Cmd {
	board, filter := m.board, m.filter
	return func() tea.Msg {
		rows, err := board.List(filter)
		if err != nil {
			return rowsFailedMsg{err: err}
		}
		return rowsLoadedMsg{rows: rows}
	}
}

func (m BrowseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.rows.SetSize(m.width, m.listHeight())
		m.statuses.SetSize(m.width, m.listHeight())
		return m, nil

	case rowsLoadedMsg:
		items := make([]list.Item, 0, len(msg.rows))
		for _, r := range msg.rows {
			items = append(items, rowItem{row: r})
		}
		// SetItems, not a fresh list: a reload keeps the cursor where the user
		// left it instead of yanking them back to the top.
		m.rows.SetItems(items)
		m.stage = browseList
		m.err = nil
		if len(msg.rows) == 0 {
			m.note = "no matching tasks"
		}
		return m, nil

	case rowsFailedMsg:
		if m.stage == browseLoading {
			// Nothing on screen to fall back to, so this one is terminal.
			m.err = msg.err
			m.stage = browseFatal
			return m, nil
		}
		// A failed refresh leaves the rows already on screen alone: stale data
		// the user can still read beats an empty screen.
		m.note = "reload failed: " + msg.err.Error()
		m.stage = browseList
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.note = msg.err.Error()
			return m, nil
		}
		m.note = msg.note
		if msg.reload {
			return m, m.loadRows()
		}
		return m, nil

	case tea.KeyMsg:
		return m.updateBrowseKey(msg)
	}
	return m, nil
}

func (m BrowseModel) updateBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	switch m.stage {
	case browseList:
		return m.updateList(msg)
	case browseDetail:
		return m.updateDetail(msg)
	case browsePickStatus, browsePickFilter:
		return m.updateStatusPicker(msg)
	case browseCreate:
		return m.updateCreate(msg)
	case browseFatal:
		return m, tea.Quit
	}
	return m, nil
}

func (m BrowseModel) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.rows.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.rows, cmd = m.rows.Update(msg)
		return m, cmd
	}

	// Every keystroke answers the previous one's message, so clear it first
	// rather than leaving yesterday's note under today's screen.
	m.note = ""

	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "r":
		m.note = "reloading…"
		return m, m.loadRows()
	case "n":
		m.stage = browseCreate
		m.focus = 0
		m.ticket.SetValue("")
		m.title.SetValue("")
		m.ticket.Focus()
		m.title.Blur()
		return m, textinput.Blink
	case "f":
		return m.openStatusPicker(browsePickFilter)
	}

	row, hasRow := m.selectedRow()
	if !hasRow {
		var cmd tea.Cmd
		m.rows, cmd = m.rows.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "enter":
		m.stage = browseDetail
		return m, nil
	case "s":
		return m.openStatusPicker(browsePickStatus)
	case "o":
		return m, m.openRow(row)
	case "y":
		return m, m.copyRow(row)
	}

	var cmd tea.Cmd
	m.rows, cmd = m.rows.Update(msg)
	return m, cmd
}

func (m BrowseModel) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	row, hasRow := m.selectedRow()
	switch msg.String() {
	case "q", "esc", "enter":
		m.stage = browseList
		return m, nil
	}
	if !hasRow {
		return m, nil
	}
	switch msg.String() {
	case "o":
		return m, m.openRow(row)
	case "y":
		return m, m.copyRow(row)
	case "s":
		return m.openStatusPicker(browsePickStatus)
	}
	return m, nil
}

// openStatusPicker loads the status values on first use and shows them. The
// values come from the live schema, so a status the board does not accept
// cannot be picked — the same guarantee `list --status` gives on the CLI.
func (m BrowseModel) openStatusPicker(next browseStage) (tea.Model, tea.Cmd) {
	if m.statusSet == nil {
		values, err := m.board.Statuses()
		if err != nil {
			m.note = "cannot read the status values: " + err.Error()
			return m, nil
		}
		m.statusSet = values
	}

	items := make([]list.Item, 0, len(m.statusSet)+1)
	title := "Move to which status?"
	if next == browsePickFilter {
		// Clearing the filter has to be reachable, and "all" is what the
		// unfiltered list already is.
		title = "Show which status?"
		items = append(items, statusItem{all: true})
	}
	for _, s := range m.statusSet {
		items = append(items, statusItem{name: s})
	}
	m.statuses = newList(items, title, m.width, m.listHeight())
	m.stage = next
	return m, nil
}

func (m BrowseModel) updateStatusPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.statuses.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.statuses, cmd = m.statuses.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "q", "esc":
		m.stage = browseList
		return m, nil
	case "enter":
		item, ok := m.statuses.SelectedItem().(statusItem)
		if !ok {
			m.stage = browseList
			return m, nil
		}
		if m.stage == browsePickFilter {
			m.filter = item.name // "" for the "all" entry
			m.stage = browseList
			return m, m.loadRows()
		}
		row, hasRow := m.selectedRow()
		m.stage = browseList
		if !hasRow {
			return m, nil
		}
		return m, m.setStatus(row, item.name)
	}

	var cmd tea.Cmd
	m.statuses, cmd = m.statuses.Update(msg)
	return m, cmd
}

func (m BrowseModel) updateCreate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.stage = browseList
		return m, nil
	case "tab", "shift+tab", "up", "down":
		m.focus = 1 - m.focus
		if m.focus == 0 {
			m.ticket.Focus()
			m.title.Blur()
		} else {
			m.title.Focus()
			m.ticket.Blur()
		}
		return m, nil
	case "enter":
		ticket := strings.TrimSpace(m.ticket.Value())
		if ticket == "" {
			// The ticket key is the row's identity: upsert keys off it, and a
			// blank one would create a row nothing can find again.
			m.note = "a ticket key is required"
			return m, nil
		}
		title := strings.TrimSpace(m.title.Value())
		status := m.filter
		m.stage = browseList
		return m, m.create(ticket, title, status)
	}

	var cmd tea.Cmd
	if m.focus == 0 {
		m.ticket, cmd = m.ticket.Update(msg)
	} else {
		m.title, cmd = m.title.Update(msg)
	}
	return m, cmd
}

func (m BrowseModel) selectedRow() (Row, bool) {
	item, ok := m.rows.SelectedItem().(rowItem)
	if !ok {
		return Row{}, false
	}
	return item.row, true
}

// The write commands below all report through actionDoneMsg, so a failure
// lands in the same one-line note as a success instead of tearing the screen
// down: the list is still readable and still correct.

func (m BrowseModel) setStatus(row Row, status string) tea.Cmd {
	board := m.board
	return func() tea.Msg {
		if err := board.SetStatus(row.PageID, status); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{note: rowLabel(row) + " → " + status, reload: true}
	}
}

func (m BrowseModel) create(ticket, title, status string) tea.Cmd {
	board := m.board
	return func() tea.Msg {
		if err := board.Create(ticket, title, status); err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{note: "created " + ticket, reload: true}
	}
}

func (m BrowseModel) openRow(row Row) tea.Cmd {
	open := m.openInBrowser
	return func() tea.Msg {
		if row.URL == "" {
			return actionDoneMsg{note: "that row has no Notion URL"}
		}
		if err := open(row.URL); err != nil {
			return actionDoneMsg{err: fmt.Errorf("could not open the browser: %w", err)}
		}
		return actionDoneMsg{note: "opened in Notion"}
	}
}

func (m BrowseModel) copyRow(row Row) tea.Cmd {
	copyFn := m.copyToClipboard
	return func() tea.Msg {
		if row.URL == "" {
			return actionDoneMsg{note: "that row has no Notion URL"}
		}
		if err := copyFn(row.URL); err != nil {
			// A headless Linux box with no xclip is a normal place to run
			// this, not a broken one.
			return actionDoneMsg{err: fmt.Errorf("could not reach the clipboard: %w", err)}
		}
		return actionDoneMsg{note: "URL copied"}
	}
}

func (m BrowseModel) View() string {
	switch m.stage {
	case browseLoading:
		return "\n  loading…\n"
	case browseFatal:
		return "\n" + warnStyle.Render("  could not read the board") +
			fmt.Sprintf("\n  %v\n\n", m.err) + hintStyle.Render("  any key  quit")
	case browsePickStatus, browsePickFilter:
		return m.statuses.View() + "\n" + hintStyle.Render("  enter  choose    esc  back")
	case browseDetail:
		return m.detailView()
	case browseCreate:
		return m.createView()
	}
	return m.listView()
}

func (m BrowseModel) listView() string {
	var b strings.Builder
	b.WriteString(m.rows.View())
	b.WriteString("\n")
	if m.filter != "" {
		b.WriteString(hintStyle.Render("  filter: "+m.filter) + "\n")
	}
	if m.note != "" {
		b.WriteString("  " + m.note + "\n")
	}
	b.WriteString(hintStyle.Render(
		"  enter  detail    s  status    f  filter    n  new    o  open    y  copy    r  reload    q  quit"))
	return b.String()
}

func (m BrowseModel) detailView() string {
	row, ok := m.selectedRow()
	if !ok {
		return "\n  nothing selected\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s\n\n", titleStyle.Render(rowLabel(row)))
	fmt.Fprintf(&b, "  %-10s %s\n", "title", orDash(row.Title))
	fmt.Fprintf(&b, "  %-10s %s\n", "ticket", orDash(row.Ticket))
	fmt.Fprintf(&b, "  %-10s %s\n", "status", orDash(row.Status))
	fmt.Fprintf(&b, "  %-10s %s\n", "due", orDash(row.Due))
	fmt.Fprintf(&b, "  %-10s %s\n", "url", orDash(row.URL))
	if m.note != "" {
		b.WriteString("\n  " + m.note + "\n")
	}
	b.WriteString("\n" + hintStyle.Render("  s  status    o  open    y  copy    esc  back"))
	return b.String()
}

func (m BrowseModel) createView() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s\n\n", titleStyle.Render("New task"))
	fmt.Fprintf(&b, "  ticket  %s\n", m.ticket.View())
	fmt.Fprintf(&b, "  title   %s\n", m.title.View())
	if m.filter != "" {
		// Creating from a filtered view lands the row in the view you are
		// looking at, which is nearly always what was meant.
		fmt.Fprintf(&b, "\n  %s\n", hintStyle.Render("status: "+m.filter+" (the active filter)"))
	}
	if m.note != "" {
		b.WriteString("\n  " + warnStyle.Render(m.note) + "\n")
	}
	b.WriteString("\n" + hintStyle.Render("  enter  create    tab  next field    esc  cancel"))
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// rowLabel names a row in one string. A board keyed by task name maps the
// ticket and the title onto one column, and repeating an identical value would
// read as a display bug — the same reasoning as list's human output.
func rowLabel(r Row) string {
	if r.Ticket == "" || r.Ticket == r.Title {
		return orDash(r.Title)
	}
	if r.Title == "" {
		return r.Ticket
	}
	return r.Ticket + "  " + r.Title
}

// rowItem is one row in the list.
type rowItem struct{ row Row }

func (i rowItem) FilterValue() string {
	return i.row.Ticket + " " + i.row.Title + " " + i.row.Status
}

// line is the single line the delegate renders: identity, then status, then
// due date, each in a fixed column so the eye can scan down them.
func (i rowItem) line() string {
	return fmt.Sprintf("%-46s %-16s %s",
		truncate(rowLabel(i.row), 46), truncate(i.row.Status, 16), i.row.Due)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

var selectedStyle = lipgloss.NewStyle().Bold(true)

// rowDelegate renders one row per line. The stock delegate spends two lines
// and a blank one per entry, which on a full board means most of the screen is
// spacing.
type rowDelegate struct{}

func (rowDelegate) Height() int                         { return 1 }
func (rowDelegate) Spacing() int                        { return 0 }
func (rowDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (rowDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	row, ok := item.(rowItem)
	if !ok {
		return
	}
	if index == m.Index() {
		fmt.Fprint(w, selectedStyle.Render("▸ "+row.line()))
		return
	}
	fmt.Fprint(w, "  "+row.line())
}

// statusItem is one status value in a picker. The zero value with all set is
// the "no filter" entry.
type statusItem struct {
	name string
	all  bool
}

func (i statusItem) Title() string {
	if i.all {
		return "— all —"
	}
	return i.name
}

func (i statusItem) Description() string {
	if i.all {
		return "clear the filter"
	}
	return ""
}

func (i statusItem) FilterValue() string { return i.name }
