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

// identitySchema is guessableSchema plus a select column of people. Its name
// is not one GuessMapping recognises, so a test that wants the assignee role
// mapped has to map it the way a user does — which is the only route to the
// identity step.
func identitySchema(options ...string) *notion.Schema {
	s := guessableSchema()
	s.Properties["Chi"] = notion.Property{Name: "Chi", Type: "select", Options: options}
	return s
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

func TestWizardAssigneeRole(t *testing.T) {
	t.Run("the role is offered and optional", func(t *testing.T) {
		var spec roleSpec
		var found bool
		for _, r := range roles {
			if r.name == "assignee" {
				spec, found = r, true
			}
		}
		if !found {
			t.Fatal("no assignee role in the wizard")
		}
		if !spec.optional {
			t.Error("the assignee role must be optional: a board may track nobody")
		}
		if len(spec.types) != 1 || spec.types[0] != "select" {
			t.Errorf("types = %v, want [select]", spec.types)
		}
		if spec.key == "" {
			t.Error("the role has no shortcut key")
		}
	})

	t.Run("roleValue and setRole round-trip", func(t *testing.T) {
		var p config.Properties
		setRole(&p, "assignee", "Referente")
		if p.Assignee != "Referente" {
			t.Errorf("setRole left Assignee = %q", p.Assignee)
		}
		if got := roleValue(p, "assignee"); got != "Referente" {
			t.Errorf("roleValue = %q, want %q", got, "Referente")
		}
	})

	t.Run("no two roles share a shortcut key", func(t *testing.T) {
		seen := map[string]string{}
		for _, r := range roles {
			if other, dup := seen[r.key]; dup {
				t.Errorf("key %q is used by both %q and %q", r.key, other, r.name)
			}
			seen[r.key] = r.name
		}
	})
}

func TestWizardPriorityRole(t *testing.T) {
	var spec roleSpec
	var found bool
	for _, r := range roles {
		if r.name == "priority" {
			spec, found = r, true
		}
	}
	if !found {
		t.Fatal("no priority role in the wizard")
	}
	if !spec.optional {
		t.Error("the priority role must be optional")
	}
	if len(spec.types) != 1 || spec.types[0] != "select" {
		t.Errorf("types = %v, want [select]", spec.types)
	}

	var p config.Properties
	setRole(&p, "priority", "Urgenza")
	if p.Priority != "Urgenza" {
		t.Errorf("setRole left Priority = %q", p.Priority)
	}
	if got := roleValue(p, "priority"); got != "Urgenza" {
		t.Errorf("roleValue = %q, want %q", got, "Urgenza")
	}

	seen := map[string]string{}
	for _, r := range roles {
		if other, dup := seen[r.key]; dup {
			t.Errorf("key %q is used by both %q and %q", r.key, other, r.name)
		}
		seen[r.key] = r.name
	}
}

// withAssignee maps the assignee role onto "Chi": the picker offers
// "— none —" first, then the only select column, so down-then-enter picks it.
func withAssignee(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = press(t, m, "a", "down", "enter")
	if m.props.Assignee != "Chi" {
		t.Fatalf("assignee = %q, want Chi", m.props.Assignee)
	}
	return m
}

// An identity resolves against the assignee column, so a board that tracks
// nobody must never be asked for one — the question would have no answers.
func TestWizardSkipsTheIdentityStepWhenAssigneeIsUnmapped(t *testing.T) {
	m := atConfirm(t, identitySchema("Jordan Lee", "Sam Rivera"))
	if m.props.Assignee != "" {
		t.Fatalf("assignee = %q, want the guess to have declined", m.props.Assignee)
	}

	saved, cmd := press(t, m, "enter")

	if saved.stage != stageDone || cmd == nil {
		t.Fatalf("stage = %v, want enter to have saved straight away", saved.stage)
	}
	res := saved.Result()
	if res.Cancelled || res.Err != nil {
		t.Fatalf("result = %+v, want a saved mapping", res)
	}
	if res.Identity != "" {
		t.Errorf("identity = %q, want none: the wizard never asked", res.Identity)
	}
}

// A column with no options offers nothing to pick, so the step would be a
// list holding only its own escape hatch.
func TestWizardSkipsTheIdentityStepWhenTheAssigneeColumnHasNoOptions(t *testing.T) {
	m := withAssignee(t, atConfirm(t, identitySchema()))

	saved, cmd := press(t, m, "enter")

	if saved.stage != stageDone || cmd == nil {
		t.Fatalf("stage = %v, want enter to have saved straight away", saved.stage)
	}
	if got := saved.Result().Identity; got != "" {
		t.Errorf("identity = %q, want none", got)
	}
}

func TestWizardCollectsTheIdentityWhenAssigneeIsMapped(t *testing.T) {
	m := withAssignee(t, atConfirm(t, identitySchema("Jordan Lee", "Sam Rivera")))

	asking, cmd := press(t, m, "enter")

	if asking.stage != stageIdentity {
		t.Fatalf("stage = %v, want the identity picker", asking.stage)
	}
	if cmd != nil {
		t.Fatal("the wizard quit instead of asking for an identity")
	}
	view := asking.View()
	for _, want := range []string{"Chi", "Jordan Lee", "Sam Rivera"} {
		if !strings.Contains(view, want) {
			t.Errorf("the identity screen does not show %q:\n%s", want, view)
		}
	}

	// The list leads with the skip entry, so the first option is one down.
	saved, cmd := press(t, asking, "down", "enter")

	if saved.stage != stageDone || cmd == nil {
		t.Fatalf("stage = %v, want the wizard finished", saved.stage)
	}
	res := saved.Result()
	if res.Cancelled || res.Err != nil {
		t.Fatalf("result = %+v, want a saved mapping", res)
	}
	if res.Identity != "Jordan Lee" {
		t.Errorf("identity = %q, want Jordan Lee", res.Identity)
	}
	if res.Props.Assignee != "Chi" {
		t.Errorf("assignee = %q, want the mapping to have survived the step", res.Props.Assignee)
	}
}

// The picker only ever offers what the column holds, so a name that is not an
// option of it cannot be produced at all.
func TestTheIdentityPickerOffersOnlyTheColumnsOwnOptions(t *testing.T) {
	m := withAssignee(t, atConfirm(t, identitySchema("Jordan Lee", "Sam Rivera")))
	asking, _ := press(t, m, "enter")

	var got []string
	for _, item := range asking.identityList.Items() {
		got = append(got, item.(identityItem).Title())
	}
	want := []string{"— skip —", "Jordan Lee", "Sam Rivera"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("picker offers %v, want %v", got, want)
	}
}

// Having no identity is a normal setup — doctor reports it as ok — so
// skipping has to finish the wizard, not cancel it.
func TestWizardIdentityCanBeSkipped(t *testing.T) {
	t.Run("by choosing the skip entry", func(t *testing.T) {
		m := withAssignee(t, atConfirm(t, identitySchema("Jordan Lee", "Sam Rivera")))
		asking, _ := press(t, m, "enter")

		saved, cmd := press(t, asking, "enter")

		if saved.stage != stageDone || cmd == nil {
			t.Fatalf("stage = %v, want the wizard finished", saved.stage)
		}
		res := saved.Result()
		if res.Cancelled || res.Err != nil {
			t.Fatalf("result = %+v, want a saved mapping", res)
		}
		if res.Identity != "" {
			t.Errorf("identity = %q, want none", res.Identity)
		}
	})

	// esc goes back to the summary, as it does out of every other picker. The
	// question must not come back on the next enter, or enter would be a loop
	// only answering could break out of.
	t.Run("by escaping back to the summary", func(t *testing.T) {
		m := withAssignee(t, atConfirm(t, identitySchema("Jordan Lee", "Sam Rivera")))
		asking, _ := press(t, m, "enter")

		back, cmd := press(t, asking, "esc")
		if back.stage != stageConfirm || cmd != nil {
			t.Fatalf("stage = %v, want back at the summary without quitting", back.stage)
		}

		saved, cmd := press(t, back, "enter")

		if saved.stage != stageDone || cmd == nil {
			t.Fatalf("stage = %v, want enter to have saved this time", saved.stage)
		}
		if got := saved.Result().Identity; got != "" {
			t.Errorf("identity = %q, want none", got)
		}
	})
}

// Ctrl-C means the same thing on every screen, and a cancelled run hands back
// nothing a caller could mistake for an approved setup.
func TestCancellingTheIdentityStepWritesNothing(t *testing.T) {
	m := withAssignee(t, atConfirm(t, identitySchema("Jordan Lee", "Sam Rivera")))
	asking, _ := press(t, m, "enter")

	cancelled, cmd := press(t, asking, "ctrl+c")

	if cmd == nil {
		t.Fatal("ctrl+c did not quit")
	}
	res := cancelled.Result()
	if !res.Cancelled {
		t.Fatalf("result = %+v, want cancelled", res)
	}
	if res.Identity != "" || res.Props != (config.Properties{}) {
		t.Errorf("cancelled result carries data: %+v", res)
	}
}

func TestRoleAccessorsRoundTripEveryRole(t *testing.T) {
	// Every role must survive setRole -> roleValue. A role added to the slice
	// but forgotten in one of the two switches would otherwise be silently
	// unsettable, and the wizard would show it as unmapped no matter what the
	// user picked.
	var p config.Properties
	for _, r := range roles {
		setRole(&p, r.name, "col-"+r.name)
	}
	for _, r := range roles {
		if got := roleValue(p, r.name); got != "col-"+r.name {
			t.Errorf("roleValue(%q) = %q, want %q", r.name, got, "col-"+r.name)
		}
	}
}
