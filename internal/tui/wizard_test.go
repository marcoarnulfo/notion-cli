package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

// The wizard is exercised the way bubbletea itself drives it — synthetic
// messages through Update — so these tests need no terminal, no network and
// no timing, and they assert on state rather than on pixels.

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// press feeds key presses in order and returns the model plus the command the
// last one produced.
func press(t *testing.T, m Model, keys ...string) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.Update(keyMsg(k))
		var ok bool
		m, ok = next.(Model)
		if !ok {
			t.Fatalf("Update returned %T, not a Model", next)
		}
	}
	return m, cmd
}

func send(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	out, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, not a Model", next)
	}
	return out
}

var testSources = []notion.DataSourceRef{
	{ID: "ds1", Title: "Tasks", DatabaseID: "db1"},
	{ID: "ds2", Title: "Bugs", DatabaseID: "db2"},
}

// guessableSchema is a board GuessMapping can map on its own: one title, a
// column named "Ticket", one named "Stato", one named "Scadenza".
func guessableSchema() *notion.Schema {
	return &notion.Schema{
		DataSourceID: "ds1",
		Title:        "Tasks",
		Properties: map[string]notion.Property{
			"Name":     {Name: "Name", Type: "title"},
			"Ticket":   {Name: "Ticket", Type: "rich_text"},
			"Notes":    {Name: "Notes", Type: "rich_text"},
			"Stato":    {Name: "Stato", Type: "status"},
			"Scadenza": {Name: "Scadenza", Type: "date"},
		},
	}
}

// ambiguousSchema has two select columns and no recognisable status name, so
// GuessMapping deliberately leaves status empty rather than guessing wrong.
func ambiguousSchema() *notion.Schema {
	return &notion.Schema{
		DataSourceID: "ds1",
		Title:        "Tasks",
		Properties: map[string]notion.Property{
			"Name":      {Name: "Name", Type: "title"},
			"Ticket":    {Name: "Ticket", Type: "rich_text"},
			"Priorità":  {Name: "Priorità", Type: "select"},
			"Categoria": {Name: "Categoria", Type: "select"},
		},
	}
}

func wizardWith(schema *notion.Schema) Model {
	return NewWizard(testSources, func(string) (*notion.Schema, error) { return schema, nil })
}

// atConfirm walks the wizard to the summary screen the way a user does:
// pick a data source, wait for the schema.
func atConfirm(t *testing.T, schema *notion.Schema) Model {
	t.Helper()
	m, cmd := press(t, wizardWith(schema), "enter")
	if cmd == nil {
		t.Fatal("selecting a data source produced no command to load the schema")
	}
	return send(t, m, cmd())
}

func TestSelectingADataSourceLoadsItsSchema(t *testing.T) {
	m, cmd := press(t, wizardWith(guessableSchema()), "down", "enter")

	if m.stage != stageLoadingSchema {
		t.Fatalf("stage = %v, want loading", m.stage)
	}
	if m.ref.ID != "ds2" {
		t.Errorf("ref = %+v, want the second source", m.ref)
	}
	// The database id rides along on the ref, which is why the wizard never
	// has to ask for it.
	if m.ref.DatabaseID != "db2" {
		t.Errorf("database id = %q, want db2", m.ref.DatabaseID)
	}
	if _, ok := cmd().(schemaLoadedMsg); !ok {
		t.Error("the command did not produce a loaded schema")
	}
}

func TestTheGuessedMappingIsWhatTheSummaryProposes(t *testing.T) {
	m := atConfirm(t, guessableSchema())

	if m.stage != stageConfirm {
		t.Fatalf("stage = %v, want confirm", m.stage)
	}
	if m.props.Ticket != "Ticket" || m.props.Status != "Stato" ||
		m.props.Title != "Name" || m.props.Due != "Scadenza" {
		t.Fatalf("props = %+v", m.props)
	}
	view := m.View()
	for _, want := range []string{"Tasks", "Ticket", "Stato", "Name", "Scadenza"} {
		if !strings.Contains(view, want) {
			t.Errorf("summary does not show %q:\n%s", want, view)
		}
	}
}

// A profile missing ticket, status or title is broken on first use — every
// lookup keys off them — so the wizard must not be able to write one.
func TestSavingIsBlockedUntilTheRequiredRolesAreMapped(t *testing.T) {
	m := atConfirm(t, ambiguousSchema())

	if m.props.Status != "" {
		t.Fatalf("status = %q, want the guess to have declined", m.props.Status)
	}
	if m.canSave() {
		t.Fatal("canSave with status unmapped")
	}
	if !strings.Contains(m.View(), "map status before saving") {
		t.Errorf("the screen does not say what is missing:\n%s", m.View())
	}

	after, cmd := press(t, m, "enter")
	if after.stage == stageDone || cmd != nil {
		t.Fatal("enter saved a profile with a required role unmapped")
	}
}

func TestMappingTheMissingRoleUnblocksSaving(t *testing.T) {
	m := atConfirm(t, ambiguousSchema())

	// "s" opens the status picker; the first entry of a sorted list of the two
	// select columns is Categoria.
	m, _ = press(t, m, "s", "enter")

	if m.stage != stageConfirm {
		t.Fatalf("stage = %v, want back at the summary", m.stage)
	}
	if m.props.Status != "Categoria" {
		t.Fatalf("status = %q, want Categoria", m.props.Status)
	}
	if !m.canSave() {
		t.Fatal("still cannot save with every required role mapped")
	}

	saved, cmd := press(t, m, "enter")
	if saved.stage != stageDone || cmd == nil {
		t.Fatalf("enter did not save: stage = %v", saved.stage)
	}
	res := saved.Result()
	if res.Cancelled || res.Err != nil {
		t.Fatalf("result = %+v, want a saved mapping", res)
	}
	if res.Props.Status != "Categoria" || res.Ref.ID != "ds1" || res.Schema == nil {
		t.Errorf("result = %+v", res)
	}
}

// Offering only columns of a usable type is what makes an invalid mapping
// impossible to express, instead of something to reject after the fact.
func TestARolePickerOffersOnlyColumnsOfAUsableType(t *testing.T) {
	m := atConfirm(t, guessableSchema())

	cases := []struct {
		key  string
		want []string
	}{
		// title accepts only title columns, so Ticket and Notes must not be
		// offered even though they are text.
		{"i", []string{"Name"}},
		// ticket accepts rich_text and title alike.
		{"t", []string{"Name", "Notes", "Ticket"}},
		{"s", []string{"Stato"}},
		// due is optional, so its picker leads with a way to unset it.
		{"d", []string{"— none —", "Scadenza"}},
	}
	for _, tc := range cases {
		opened, _ := press(t, m, tc.key)
		if opened.stage != stageEditRole {
			t.Fatalf("%q did not open a picker", tc.key)
		}
		var got []string
		for _, item := range opened.roleList.Items() {
			got = append(got, item.(propItem).Title())
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("picker %q offers %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestTheOptionalRoleCanBeUnset(t *testing.T) {
	m := atConfirm(t, guessableSchema())
	if m.props.Due == "" {
		t.Fatal("due was not guessed, so unsetting it proves nothing")
	}

	// The first entry of the due picker is "— none —".
	m, _ = press(t, m, "d", "enter")

	if m.props.Due != "" {
		t.Fatalf("due = %q, want it cleared", m.props.Due)
	}
	if !m.canSave() {
		t.Error("clearing an optional role blocked saving")
	}
}

// Escaping out of a picker means "never mind", not "clear this role".
func TestLeavingAPickerKeepsTheCurrentMapping(t *testing.T) {
	m := atConfirm(t, guessableSchema())

	m, _ = press(t, m, "t", "down", "esc")

	if m.stage != stageConfirm {
		t.Fatalf("stage = %v, want confirm", m.stage)
	}
	if m.props.Ticket != "Ticket" {
		t.Errorf("ticket = %q, want it untouched", m.props.Ticket)
	}
}

func TestCancellingWritesNothing(t *testing.T) {
	for _, key := range []string{"esc", "q", "ctrl+c"} {
		m, cmd := press(t, atConfirm(t, guessableSchema()), key)

		if cmd == nil {
			t.Errorf("%q did not quit", key)
		}
		res := m.Result()
		if !res.Cancelled {
			t.Errorf("%q: result = %+v, want cancelled", key, res)
		}
		// A cancelled run must not hand back a half-built mapping that a
		// caller might mistake for an approved one.
		if res.Props != (config.Properties{}) {
			t.Errorf("%q: cancelled result carries a mapping: %+v", key, res.Props)
		}
	}
}

func TestCtrlCQuitsFromTheFirstScreen(t *testing.T) {
	m, cmd := press(t, wizardWith(guessableSchema()), "ctrl+c")

	if cmd == nil {
		t.Fatal("ctrl+c did not quit")
	}
	if !m.Result().Cancelled {
		t.Errorf("result = %+v, want cancelled", m.Result())
	}
}

func TestAFailedSchemaReadCanBeRecoveredFrom(t *testing.T) {
	m, _ := press(t, wizardWith(guessableSchema()), "enter")
	m = send(t, m, schemaFailedMsg{err: errors.New("data source not shared")})

	if m.stage != stageSchemaError {
		t.Fatalf("stage = %v, want the error screen", m.stage)
	}
	if !strings.Contains(m.View(), "data source not shared") {
		t.Errorf("the error screen hides the reason:\n%s", m.View())
	}

	back, _ := press(t, m, "esc")
	if back.stage != stagePickSource {
		t.Fatalf("stage = %v, want back at the picker", back.stage)
	}
	if back.err != nil {
		t.Error("the stale error survived going back")
	}
}

// Quitting from the error screen has to surface the failure, not report a
// cancellation: nothing the user did caused it.
func TestQuittingOnAFailedSchemaReadReportsTheError(t *testing.T) {
	m, _ := press(t, wizardWith(guessableSchema()), "enter")
	m = send(t, m, schemaFailedMsg{err: errors.New("data source not shared")})

	m, cmd := press(t, m, "q")

	if cmd == nil {
		t.Fatal("q did not quit")
	}
	res := m.Result()
	if res.Err == nil || !strings.Contains(res.Err.Error(), "data source not shared") {
		t.Fatalf("result = %+v, want the schema error", res)
	}
}
