package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

func findCheck(checks []Check, name string) (Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func TestDoctorReportsAHealthySetup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track","type":"bot"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	}))
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	for _, c := range checks {
		if c.Status == "fail" {
			t.Errorf("check %q failed: %s", c.Name, c.Detail)
		}
	}
}

func TestDoctorReportsAnInvalidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":"unauthorized","message":"API token is invalid."}`))
	}))
	defer srv.Close()

	checks := New(notion.New("bad", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	c, ok := findCheck(checks, "token")
	if !ok || c.Status != "fail" {
		t.Fatalf("token check = %+v", c)
	}
}

func TestDoctorSpotsARenamedProperty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			// "Stato" is gone; "Status" took its place.
			w.Write([]byte(`{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{
				"Name":{"name":"Name","type":"title","title":{}},
				"Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
				"Status":{"name":"Status","type":"status","status":{"options":[{"name":"Fatto"}]}}
			}}`))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	}))
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	c, ok := findCheck(checks, "properties")
	if !ok || c.Status != "fail" {
		t.Fatalf("properties check = %+v", c)
	}
	// The whole value of this check is naming the likely replacement.
	if !strings.Contains(c.Detail, "Stato") || !strings.Contains(c.Detail, "Status") {
		t.Errorf("detail does not point at the rename: %s", c.Detail)
	}
}

func TestDoctorListsDuplicateTickets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		default:
			w.Write([]byte(`{"results":[` + rowJSON + `,` + rowJSON + `],"has_more":false}`))
		}
	}))
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	c, ok := findCheck(checks, "duplicates")
	if !ok || c.Status != "fail" {
		t.Fatalf("duplicates check = %+v", c)
	}
	if !strings.Contains(c.Detail, "BDF-231") || !strings.Contains(c.Detail, "https://notion.so/page1") {
		t.Errorf("detail does not identify the duplicates: %s", c.Detail)
	}
}

// Regression for review finding #1: an unmapped required property must not be
// skipped in silence. checkProperties has to fail loudly, and checkDuplicates
// must refuse to scan without a ticket property — otherwise every row's
// lookup on "" resolves to "" and the check reports a reassuring "ok" over a
// broken mapping.
func TestDoctorFlagsAnUnmappedRequiredProperty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		default:
			// Duplicate rows to prove the duplicates check would otherwise have
			// found a real problem, not just been silent for lack of rows.
			w.Write([]byte(`{"results":[` + rowJSON + `,` + rowJSON + `],"has_more":false}`))
		}
	}))
	defer srv.Close()

	profile := testProfile()
	profile.Properties.Ticket = ""

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), profile).Doctor(context.Background())

	props, ok := findCheck(checks, "properties")
	if !ok || props.Status != "fail" {
		t.Fatalf("properties check = %+v, want fail", props)
	}
	if !strings.Contains(props.Detail, "ticket") || !strings.Contains(props.Detail, "notion-track init") {
		t.Errorf("detail does not explain the missing mapping: %s", props.Detail)
	}

	dup, ok := findCheck(checks, "duplicates")
	if !ok || dup.Status != "fail" {
		t.Fatalf("duplicates check = %+v, want fail without a ticket property configured", dup)
	}
}

// Regression for review finding #2: a status_type drift is explicitly
// documented as "not fatal" but used to fail the whole properties check. When
// it is the only problem found, the check must warn, not fail.
func TestDoctorWarnsOnAStatusTypeChangeAlone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			// Stato was a "status" property when init ran (see testProfile); it
			// is now a "select". Still a valid type for the role, so this is the
			// only problem checkProperties should find.
			w.Write([]byte(`{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{
				"Name":{"name":"Name","type":"title","title":{}},
				"Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
				"Stato":{"name":"Stato","type":"select","select":{"options":[{"name":"Fatto"}]}}
			}}`))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	}))
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	c, ok := findCheck(checks, "properties")
	if !ok || c.Status != "warn" {
		t.Fatalf("properties check = %+v, want warn", c)
	}
	if !strings.Contains(c.Detail, "changed type") || !strings.Contains(c.Detail, "since init") {
		t.Errorf("detail does not explain the status_type change: %s", c.Detail)
	}
}

// Mutation-testing gap: checkProperties never had a test for a property that
// exists but carries the wrong type. Deleting that branch left the suite
// green.
func TestDoctorFlagsAPropertyWithTheWrongType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			// Ticket used to be text; someone turned it into a number column.
			w.Write([]byte(`{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{
				"Name":{"name":"Name","type":"title","title":{}},
				"Ticket":{"name":"Ticket","type":"number","number":{}},
				"Stato":{"name":"Stato","type":"status","status":{"options":[{"name":"Fatto"}]}}
			}}`))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	}))
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	c, ok := findCheck(checks, "properties")
	if !ok || c.Status != "fail" {
		t.Fatalf("properties check = %+v, want fail", c)
	}
	if !strings.Contains(c.Detail, `type "number"`) {
		t.Errorf("detail does not name the wrong type: %s", c.Detail)
	}
}

// Regression for review finding #3: a data source failure must not skip
// checks that do not need the schema. checkDuplicates is one of those, and it
// already knows how to degrade to "warn" on its own if the row query fails
// too, so there is no reason to hide it behind data_source.
func TestDoctorRunsDuplicatesCheckAfterADataSourceFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"code":"restricted_resource","message":"not shared with the integration"}`))
		default:
			// The duplicates check does not use the schema; it should still run
			// and still be able to find a real duplicate here.
			w.Write([]byte(`{"results":[` + rowJSON + `,` + rowJSON + `],"has_more":false}`))
		}
	}))
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())

	ds, ok := findCheck(checks, "data_source")
	if !ok || ds.Status != "fail" {
		t.Fatalf("data_source check = %+v, want fail", ds)
	}
	dup, ok := findCheck(checks, "duplicates")
	if !ok {
		t.Fatal("duplicates check did not run after the data source check failed")
	}
	if dup.Status != "fail" {
		t.Fatalf("duplicates check = %+v, want fail (it should have found the seeded duplicate)", dup)
	}
	// properties needs the schema, which is unavailable here: it must not run.
	if _, ok := findCheck(checks, "properties"); ok {
		t.Error("properties check ran without a schema")
	}
}

// doctorRoutes answers the three endpoints Doctor touches.
func doctorRoutes(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track","type":"bot"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	}))
}

func TestDoctorTreatsTheAssigneeAsOptional(t *testing.T) {
	srv := doctorRoutes(t)
	defer srv.Close()

	// testProfile leaves the role unmapped, the way every profile written
	// before this feature does.
	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).
		Doctor(context.Background())

	props, ok := findCheck(checks, "properties")
	if !ok {
		t.Fatal("no properties check")
	}
	if props.Status == "fail" {
		t.Errorf("properties = fail (%s), want the optional role to be skipped", props.Detail)
	}
	if _, ok := findCheck(checks, "assignee"); ok {
		t.Error("an assignee check ran for a profile that does not map the role")
	}
}

func TestDoctorWarnsWhenTheIdentityNoLongerResolves(t *testing.T) {
	// The developer running the suite may well have exported NOTION_TRACK_ME
	// themselves; clear it so the outcome depends only on the fixture below.
	t.Setenv(config.MeEnv, "")

	srv := doctorRoutes(t)
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("Someone Who Left")).
		Doctor(context.Background())

	check, ok := findCheck(checks, "assignee")
	if !ok {
		t.Fatal("no assignee check")
	}
	if check.Status != "warn" {
		t.Errorf("assignee = %s (%s), want warn", check.Status, check.Detail)
	}
}

func TestDoctorAcceptsAnIdentityFromTheEnvironment(t *testing.T) {
	srv := doctorRoutes(t)
	defer srv.Close()
	t.Setenv(config.MeEnv, "mirko")

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("mirko")).
		Doctor(context.Background())

	check, ok := findCheck(checks, "assignee")
	if !ok {
		t.Fatal("no assignee check")
	}
	if check.Status != "ok" {
		t.Errorf("assignee = %s (%s), want ok", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "Mirko Spinato") {
		t.Errorf("detail = %q, want it to name who me resolves to", check.Detail)
	}
}

func TestDoctorAcceptsAMappedRoleWithNoIdentity(t *testing.T) {
	// Mapping the column without configuring an identity is a legitimate
	// setup — it only means "--assignee me" is unavailable — so it must read
	// as ok, not as something to fix.
	srv := doctorRoutes(t)
	defer srv.Close()
	t.Setenv(config.MeEnv, "")

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("")).
		Doctor(context.Background())

	check, ok := findCheck(checks, "assignee")
	if !ok {
		t.Fatal("no assignee check")
	}
	if check.Status != "ok" {
		t.Errorf("assignee = %s (%s), want ok", check.Status, check.Detail)
	}
}

func TestDoctorWarnsWhenTheMappedColumnIsGone(t *testing.T) {
	// Without the missing-column guard, ResolveOption would be handed a
	// zero-value Property and the warning would trail off into "allowed values
	// are:" with nothing after it — naming neither the cause nor a fix.
	srv := doctorRoutes(t)
	defer srv.Close()
	t.Setenv(config.MeEnv, "")

	profile := assigneeProfile("Mirko Spinato")
	profile.Properties.Assignee = "Referente rinominato"

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), profile).
		Doctor(context.Background())

	check, ok := findCheck(checks, "assignee")
	if !ok {
		t.Fatal("no assignee check")
	}
	if check.Status != "warn" {
		t.Errorf("assignee = %s (%s), want warn", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "Referente rinominato") {
		t.Errorf("detail = %q, want it to name the column that disappeared", check.Detail)
	}
	if strings.Contains(check.Detail, "allowed values are:") {
		t.Errorf("detail = %q, still shows the empty allowed-values list", check.Detail)
	}
}

func TestDoctorWarnsWhenTheIdentityLivesOnlyInTheSharedConfig(t *testing.T) {
	// The failure this catches is silent by nature: everything resolves, and
	// every teammate assigns work to whoever ran init.
	srv := doctorRoutes(t)
	defer srv.Close()
	t.Setenv(config.MeEnv, "")

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("mirko")).
		Doctor(context.Background())

	check, ok := findCheck(checks, "assignee")
	if !ok {
		t.Fatal("no assignee check")
	}
	if check.Status != "warn" {
		t.Errorf("assignee = %s (%s), want warn", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, config.MeEnv) {
		t.Errorf("detail = %q, want it to point at the environment variable", check.Detail)
	}
}

func TestDoctorTreatsThePriorityAsOptional(t *testing.T) {
	srv := doctorRoutes(t)
	defer srv.Close()
	t.Setenv(config.MeEnv, "")

	// assigneeProfile maps no priority, the way every profile written before
	// this feature does.
	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("")).
		Doctor(context.Background())

	props, ok := findCheck(checks, "properties")
	if !ok {
		t.Fatal("no properties check")
	}
	if props.Status == "fail" {
		t.Errorf("properties = fail (%s), want the optional role to be skipped", props.Detail)
	}
}

func TestDoctorChecksAMappedPriority(t *testing.T) {
	srv := doctorRoutes(t)
	defer srv.Close()
	t.Setenv(config.MeEnv, "")

	profile := assigneeProfile("")
	profile.Properties.Priority = "Urgenza rinominata"

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), profile).
		Doctor(context.Background())

	props, ok := findCheck(checks, "properties")
	if !ok {
		t.Fatal("no properties check")
	}
	if props.Status != "fail" {
		t.Errorf("properties = %s (%s), want fail for a column that is gone", props.Status, props.Detail)
	}
}
