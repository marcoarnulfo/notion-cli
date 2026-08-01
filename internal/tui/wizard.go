// Package tui holds notion-track's terminal interfaces.
//
// Everything here is a bubbletea Model and nothing here talks to Notion. The
// network arrives as injected functions, so a test can drive a whole screen
// flow with synthetic key presses and canned data — no terminal, no HTTP, no
// timing. internal/cli is the adapter that wires the real client in.
package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

// stage is which screen the wizard is showing.
type stage int

const (
	stagePickSource stage = iota
	stageLoadingSchema
	stageConfirm
	stageEditRole
	stageIdentity
	stageSchemaError
	stageDone
	stageCancelled
)

// roleSpec describes one mapped role: the key that opens its picker, the
// property types it accepts, and whether a profile can be written without it.
//
// The accepted types are the same sets validateMapping enforces in
// internal/cli/init.go. Offering only compatible columns makes the mapping
// that validateMapping would reject impossible to express in the first place,
// rather than something to catch and explain afterwards.
type roleSpec struct {
	name     string
	key      string
	types    []string
	optional bool
}

// The order here is the order the confirmation screen lists them in.
//
// title's key is "i", not "t": ticket claimed "t", and a first-run user is far
// likelier to be reaching for the ticket key than for the title, which
// GuessMapping fills in on its own in every board that has exactly one title
// column — which is every board.
var roles = []roleSpec{
	{name: "ticket", key: "t", types: []string{"rich_text", "title"}},
	{name: "status", key: "s", types: []string{"status", "select"}},
	{name: "title", key: "i", types: []string{"title"}},
	{name: "due", key: "d", types: []string{"date"}, optional: true},
	{name: "assignee", key: "a", types: []string{"select"}, optional: true},
	{name: "priority", key: "p", types: []string{"select"}, optional: true},
	// id's key is "u", for unique_id: "i" went to title, which itself only has
	// "i" because ticket claimed "t".
	{name: "id", key: "u", types: []string{"unique_id"}, optional: true},
}

// Result is what the wizard produces. Exactly one of the three outcomes holds:
// Err is set, Cancelled is true, or the mapping fields are filled in.
type Result struct {
	Ref    notion.DataSourceRef
	Schema *notion.Schema
	Props  config.Properties
	// Identity is the option of the assignee column that stands for the user,
	// what --assignee me resolves to. Empty is a complete answer: the wizard
	// only asks when an assignee column is mapped, and skipping is allowed —
	// a board can be tracked perfectly well without saying who you are.
	Identity string
	// Cancelled records that the user walked away rather than that anything
	// went wrong. The caller turns it into a distinct exit code: a script has
	// to be able to tell "profile configured" from "user changed their mind".
	Cancelled bool
	Err       error
}

// Model is the wizard. It is built with the data sources already loaded, and
// reaches back to the network exactly once — for the chosen data source's
// schema — through the injected fetchSchema.
type Model struct {
	sources     []notion.DataSourceRef
	fetchSchema func(dataSourceID string) (*notion.Schema, error)

	stage        stage
	sourceList   list.Model
	roleList     list.Model
	identityList list.Model
	editing      int // index into roles, meaningful in stageEditRole

	ref      notion.DataSourceRef
	schema   *notion.Schema
	props    config.Properties
	identity string
	// identityAsked keeps the question to one appearance per run. Without it,
	// leaving the picker without answering would put the user back on the
	// summary, where enter — the key that opened it — would open it again.
	identityAsked bool
	err           error

	width, height int
}

// Sizes used until the terminal reports its own. Most tests never send a
// WindowSizeMsg, and a list with zero height renders nothing at all, which
// would make every assertion about what is on screen vacuously fail.
const (
	defaultWidth  = 80
	defaultHeight = 20
)

// NewWizard builds the wizard over the data sources shared with the
// integration. fetchSchema is called once, for whichever one the user picks.
func NewWizard(sources []notion.DataSourceRef, fetchSchema func(string) (*notion.Schema, error)) Model {
	items := make([]list.Item, 0, len(sources))
	for _, s := range sources {
		items = append(items, sourceItem{ref: s})
	}
	m := Model{
		sources:     sources,
		fetchSchema: fetchSchema,
		stage:       stagePickSource,
		width:       defaultWidth,
		height:      defaultHeight,
	}
	m.sourceList = newList(items, "Which data source?", m.width, m.listHeight())
	// The role and identity pickers cannot be filled in yet: their contents
	// are the columns of a schema nobody has chosen and the options of a
	// column nobody has mapped. They are built for real on the way into their
	// screens — but they have to be lists from the start regardless, because
	// the terminal reports its size before the user can reach either screen,
	// and a zero list.Model has no delegate for SetSize to measure against.
	m.roleList = newList(nil, "", m.width, m.listHeight())
	m.identityList = newList(nil, "", m.width, m.listHeight())
	return m
}

func newList(items []list.Item, title string, width, height int) list.Model {
	l := list.New(items, list.NewDefaultDelegate(), width, height)
	l.Title = title
	// The wizard prints its own key hints, the same ones on every screen. The
	// built-in help would sit next to them saying the same things differently.
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	return l
}

func (m Model) listHeight() int {
	// Room for the list's own title and the wizard's footer.
	if h := m.height - 4; h > 0 {
		return h
	}
	return 1
}

// Result reports the outcome. It is meaningful once the program has quit.
func (m Model) Result() Result {
	switch {
	case m.err != nil:
		return Result{Err: m.err}
	case m.stage != stageDone:
		// Anything that is not an explicit save is a cancellation, including
		// a program torn down from outside: never report a half-built mapping
		// as one the user approved.
		return Result{Cancelled: true}
	default:
		return Result{Ref: m.ref, Schema: m.schema, Props: m.props, Identity: m.identity}
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.sourceList.SetSize(m.width, m.listHeight())
		m.roleList.SetSize(m.width, m.listHeight())
		m.identityList.SetSize(m.width, m.listHeight())
		return m, nil

	case schemaLoadedMsg:
		m.schema = msg.schema
		m.props = tracker.GuessMapping(msg.schema)
		m.stage = stageConfirm
		return m, nil

	case schemaFailedMsg:
		m.err = msg.err
		m.stage = stageSchemaError
		return m, nil

	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl-C means the same thing on every screen and must never be swallowed
	// by a list's filter input: a user who cannot get out is the one bug a TUI
	// cannot apologise for.
	if msg.Type == tea.KeyCtrlC {
		return m.cancel()
	}

	switch m.stage {
	case stagePickSource:
		return m.updatePickSource(msg)
	case stageConfirm:
		return m.updateConfirm(msg)
	case stageEditRole:
		return m.updateEditRole(msg)
	case stageIdentity:
		return m.updateIdentity(msg)
	case stageSchemaError:
		return m.updateSchemaError(msg)
	}
	return m, nil
}

func (m Model) updatePickSource(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While the filter is open every key belongs to it — "q" is a letter the
	// user is typing, not a request to quit.
	if m.sourceList.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.sourceList, cmd = m.sourceList.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "esc", "q":
		return m.cancel()
	case "enter":
		item, ok := m.sourceList.SelectedItem().(sourceItem)
		if !ok {
			// An empty list has nothing to select. The caller refuses to open
			// the wizard with no data sources, so this is only reachable if a
			// filter matched nothing.
			return m, nil
		}
		m.ref = item.ref
		m.stage = stageLoadingSchema
		return m, m.loadSchema()
	}

	var cmd tea.Cmd
	m.sourceList, cmd = m.sourceList.Update(msg)
	return m, cmd
}

func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		return m.cancel()
	case "enter":
		if !m.canSave() {
			// Refused rather than saved-and-complained-about later: a profile
			// missing a required role is broken on first use, and the screen
			// already says which one is missing.
			return m, nil
		}
		if items := m.identityItems(); !m.identityAsked && items != nil {
			// The identity is asked last, and is not a role on the summary: a
			// role names a column, this names a value inside one, so there is
			// nothing to ask until the assignee column is settled.
			m.identityList = newList(items, "Which "+m.props.Assignee+" option is you?", m.width, m.listHeight())
			m.stage = stageIdentity
			return m, nil
		}
		m.stage = stageDone
		return m, tea.Quit
	}

	for i, r := range roles {
		if msg.String() == r.key {
			m.editing = i
			m.roleList = newList(m.roleItems(r), "Which column for "+r.name+"?", m.width, m.listHeight())
			m.stage = stageEditRole
			return m, nil
		}
	}
	return m, nil
}

func (m Model) updateEditRole(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.roleList.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.roleList, cmd = m.roleList.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "esc", "q":
		// Back to the summary, leaving the mapping as it was: escaping out of
		// a picker is "never mind", not "clear this role".
		m.stage = stageConfirm
		return m, nil
	case "enter":
		if item, ok := m.roleList.SelectedItem().(propItem); ok {
			setRole(&m.props, roles[m.editing].name, item.name)
		}
		m.stage = stageConfirm
		return m, nil
	}

	var cmd tea.Cmd
	m.roleList, cmd = m.roleList.Update(msg)
	return m, cmd
}

func (m Model) updateIdentity(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.identityList.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.identityList, cmd = m.identityList.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "esc", "q":
		// Back to the summary, as out of every other picker — but the question
		// counts as asked, so the enter that opened this screen saves next
		// time instead of reopening it.
		m.identityAsked = true
		m.stage = stageConfirm
		return m, nil
	case "enter":
		if item, ok := m.identityList.SelectedItem().(identityItem); ok {
			// The skip entry's name is empty, which is exactly the value that
			// means "no identity" everywhere else.
			m.identity = item.name
		}
		m.identityAsked = true
		m.stage = stageDone
		return m, tea.Quit
	}

	var cmd tea.Cmd
	m.identityList, cmd = m.identityList.Update(msg)
	return m, cmd
}

func (m Model) updateSchemaError(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Retrying is usually pointless — the same call would fail the same
		// way — but picking a different data source often is not, so going
		// back beats dropping the user out of the wizard entirely.
		m.err = nil
		m.stage = stagePickSource
		return m, nil
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) cancel() (tea.Model, tea.Cmd) {
	m.stage = stageCancelled
	m.err = nil
	return m, tea.Quit
}

// canSave reports whether every required role is mapped.
func (m Model) canSave() bool {
	for _, r := range roles {
		if !r.optional && roleValue(m.props, r.name) == "" {
			return false
		}
	}
	return true
}

// missingRoles names the required roles still unmapped, for the screen to
// explain why enter is doing nothing.
func (m Model) missingRoles() []string {
	var out []string
	for _, r := range roles {
		if !r.optional && roleValue(m.props, r.name) == "" {
			out = append(out, r.name)
		}
	}
	return out
}

// roleItems lists the columns that can serve a role, plus an explicit "none"
// entry for the optional one so it can be unset as deliberately as it is set.
func (m Model) roleItems(r roleSpec) []list.Item {
	var names []string
	for name, p := range m.schema.Properties {
		for _, t := range r.types {
			if p.Type == t {
				names = append(names, name)
				break
			}
		}
	}
	sort.Strings(names) // map iteration order would reshuffle the list per run

	items := make([]list.Item, 0, len(names)+1)
	if r.optional {
		items = append(items, propItem{})
	}
	for _, name := range names {
		items = append(items, propItem{name: name, typ: m.schema.Properties[name].Type})
	}
	return items
}

// identityItems lists the options of the mapped assignee column, plus a way
// to answer "nobody". It returns nil when there is nothing worth asking:
// --assignee me resolves against that column, so with no column mapped — or
// no option in it — the question has no answer to offer.
func (m Model) identityItems() []list.Item {
	if m.schema == nil || m.props.Assignee == "" {
		return nil
	}
	prop, ok := m.schema.Properties[m.props.Assignee]
	if !ok || len(prop.Options) == 0 {
		return nil
	}

	// Notion's own option order is kept: unlike the role pickers, which sort
	// because map iteration would reshuffle them, this order is the one the
	// user already sees in the board.
	items := make([]list.Item, 0, len(prop.Options)+1)
	items = append(items, identityItem{})
	for _, name := range prop.Options {
		items = append(items, identityItem{name: name})
	}
	return items
}

type schemaLoadedMsg struct{ schema *notion.Schema }
type schemaFailedMsg struct{ err error }

func (m Model) loadSchema() tea.Cmd {
	id, fetch := m.ref.ID, m.fetchSchema
	return func() tea.Msg {
		schema, err := fetch(id)
		if err != nil {
			return schemaFailedMsg{err: err}
		}
		return schemaLoadedMsg{schema: schema}
	}
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	hintStyle  = lipgloss.NewStyle().Faint(true)
	warnStyle  = lipgloss.NewStyle().Bold(true)
)

func (m Model) View() string {
	switch m.stage {
	case stagePickSource:
		return m.sourceList.View() + "\n" + hintStyle.Render("enter  select    /  filter    esc  cancel")
	case stageLoadingSchema:
		return "\n  reading the schema of " + m.ref.Title + "…\n"
	case stageEditRole:
		return m.roleList.View() + "\n" + hintStyle.Render("enter  choose    /  filter    esc  back")
	case stageIdentity:
		return m.identityList.View() + "\n" + hintStyle.Render("enter  choose    /  filter    esc  skip")
	case stageSchemaError:
		return "\n" + warnStyle.Render("  could not read that data source") +
			fmt.Sprintf("\n  %v\n\n", m.err) +
			hintStyle.Render("  esc  pick another    q  quit")
	case stageConfirm:
		return m.confirmView()
	}
	return ""
}

func (m Model) confirmView() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s\n\n", titleStyle.Render("Data source: "+m.ref.Title))

	for _, r := range roles {
		value := roleValue(m.props, r.name)
		detail := ""
		switch {
		case value == "" && r.optional:
			value, detail = "— not set —", "(optional)"
		case value == "":
			value = "— not set —"
		default:
			if p, ok := m.schema.Properties[value]; ok {
				detail = "(" + p.Type + ")"
			}
		}
		fmt.Fprintf(&b, "  %s  %-8s %-24s %s\n", r.key, r.name, value, detail)
	}

	b.WriteString("\n")
	if missing := m.missingRoles(); len(missing) > 0 {
		fmt.Fprintf(&b, "  %s\n\n", warnStyle.Render(
			"map "+strings.Join(missing, " and ")+" before saving"))
		b.WriteString(hintStyle.Render("  " + keyHints() + "    esc  cancel"))
		return b.String()
	}
	b.WriteString(hintStyle.Render("  enter  save    " + keyHints() + "    esc  cancel"))
	return b.String()
}

func keyHints() string {
	keys := make([]string, 0, len(roles))
	for _, r := range roles {
		keys = append(keys, r.key)
	}
	return strings.Join(keys, "/") + "  change"
}

// roleValue and setRole are the accessors config.Properties does not have:
// the wizard addresses roles by name, since that is what the user picks off
// the screen, while the config struct addresses them by field.
func roleValue(p config.Properties, role string) string {
	switch role {
	case "ticket":
		return p.Ticket
	case "status":
		return p.Status
	case "title":
		return p.Title
	case "due":
		return p.Due
	case "assignee":
		return p.Assignee
	case "priority":
		return p.Priority
	case "id":
		return p.ID
	}
	return ""
}

func setRole(p *config.Properties, role, value string) {
	switch role {
	case "ticket":
		p.Ticket = value
	case "status":
		p.Status = value
	case "title":
		p.Title = value
	case "due":
		p.Due = value
	case "assignee":
		p.Assignee = value
	case "priority":
		p.Priority = value
	case "id":
		p.ID = value
	}
}

// sourceItem is one data source in the picker. Notion allows several data
// sources to carry the same database title, so the id is shown as the
// description: it is the only thing that tells them apart.
type sourceItem struct{ ref notion.DataSourceRef }

func (i sourceItem) Title() string       { return i.ref.Title }
func (i sourceItem) Description() string { return i.ref.ID }
func (i sourceItem) FilterValue() string { return i.ref.Title + " " + i.ref.ID }

// propItem is one column in a role picker. The zero value is the "leave this
// role unmapped" entry offered for the optional role.
type propItem struct{ name, typ string }

func (i propItem) Title() string {
	if i.name == "" {
		return "— none —"
	}
	return i.name
}

func (i propItem) Description() string {
	if i.name == "" {
		return "leave unmapped"
	}
	return i.typ
}

func (i propItem) FilterValue() string { return i.name }

// identityItem is one option of the assignee column, offered as the answer to
// "which of these is you?". Because the list is built from the column itself,
// a name that column does not offer cannot be chosen — the same guarantee
// --me gets by validating what was typed, without the validation.
//
// The zero value is the "no identity" entry, kept alongside esc so that
// skipping is as visible as choosing.
type identityItem struct{ name string }

func (i identityItem) Title() string {
	if i.name == "" {
		return "— skip —"
	}
	return i.name
}

func (i identityItem) Description() string {
	if i.name == "" {
		return "no identity; --assignee me stays unavailable"
	}
	return "--assignee me means " + i.name
}

func (i identityItem) FilterValue() string { return i.name }
