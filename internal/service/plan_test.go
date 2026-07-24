package service

import (
	"context"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

// dryRunService is a service in dry-run mode against a stub that records every
// request, so a test can assert that no write ever left the process.
func dryRunService(t *testing.T, queryResults string, seen *[]string) *Service {
	t.Helper()
	srv := routes(t, queryResults, seen)
	t.Cleanup(srv.Close)
	return New(notion.New("ntn_test", notion.WithBaseURL(srv.URL)), testProfile()).DryRun(true)
}

// wrote reports whether any request would have changed something in Notion.
func wrote(seen []string) []string {
	var writes []string
	for _, req := range seen {
		if strings.HasPrefix(req, "POST ") || strings.HasPrefix(req, "PATCH ") ||
			strings.HasPrefix(req, "DELETE ") {
			// The query endpoint is a POST that reads; everything else that is
			// not a GET changes data.
			if strings.HasSuffix(req, "/query") {
				continue
			}
			writes = append(writes, req)
		}
	}
	return writes
}

func TestDryRunUpsertOnAnExistingRowPlansAnUpdate(t *testing.T) {
	var seen []string
	svc := dryRunService(t, rowJSON, &seen)

	res, err := svc.Upsert(context.Background(),
		tracker.Fields{Ticket: "BDF-231", Status: "Fatto"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if res.Plan == nil {
		t.Fatal("no plan on a dry run")
	}
	if res.Plan.Action != "updated" {
		t.Errorf("action = %q, want updated", res.Plan.Action)
	}
	if res.Plan.PageID != "page1" {
		t.Errorf("page id = %q, want the row it found", res.Plan.PageID)
	}
	if writes := wrote(seen); len(writes) != 0 {
		t.Fatalf("a dry run wrote: %v", writes)
	}
}

func TestDryRunUpsertOnAMissingRowPlansACreate(t *testing.T) {
	var seen []string
	svc := dryRunService(t, "", &seen)

	res, err := svc.Upsert(context.Background(),
		tracker.Fields{Ticket: "BDF-999", Title: "New"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if res.Plan == nil || res.Plan.Action != "created" {
		t.Fatalf("plan = %+v, want a create", res.Plan)
	}
	// Nothing to point at yet: the row does not exist.
	if res.Plan.PageID != "" {
		t.Errorf("page id = %q, want none for a create", res.Plan.PageID)
	}
	if writes := wrote(seen); len(writes) != 0 {
		t.Fatalf("a dry run wrote: %v", writes)
	}
}

func TestDryRunSetPlansAnUpdateWithoutWriting(t *testing.T) {
	var seen []string
	svc := dryRunService(t, rowJSON, &seen)

	res, err := svc.Set(context.Background(),
		tracker.Fields{Ticket: "BDF-231", Status: "Fatto"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if res.Plan == nil || res.Plan.PageID != "page1" {
		t.Fatalf("plan = %+v", res.Plan)
	}
	if writes := wrote(seen); len(writes) != 0 {
		t.Fatalf("a dry run wrote: %v", writes)
	}
}

// Set's "the row must already exist" rule still applies: a dry run that
// reported a happy plan for a ticket set would refuse to write is worthless.
func TestDryRunSetStillFailsOnAMissingRow(t *testing.T) {
	var seen []string
	svc := dryRunService(t, "", &seen)

	_, err := svc.Set(context.Background(), tracker.Fields{Ticket: "NOPE"}, nil)

	if err == nil {
		t.Fatal("a dry run of set invented a row that does not exist")
	}
}

// The validation a real run does happens here too — a status the board does
// not accept must fail now, which is most of the point of asking first.
func TestDryRunStillRejectsAnInvalidStatus(t *testing.T) {
	var seen []string
	svc := dryRunService(t, rowJSON, &seen)

	_, err := svc.Upsert(context.Background(),
		tracker.Fields{Ticket: "BDF-231", Status: "Nonexistent"}, nil)

	if err == nil {
		t.Fatal("a dry run accepted a status the data source rejects")
	}
}

// The plan names the real columns, and only the ones actually supplied: an
// omitted field means "leave this alone" everywhere else, and listing it as
// about to be written would misrepresent what the run would do.
func TestThePlanNamesOnlyTheSuppliedColumns(t *testing.T) {
	var seen []string
	svc := dryRunService(t, rowJSON, &seen)

	res, err := svc.Upsert(context.Background(),
		tracker.Fields{Ticket: "BDF-231", Status: "Fatto"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, p := range res.Plan.Properties {
		got[p.Column] = p.Value
	}
	if got["Ticket"] != "BDF-231" || got["Stato"] != "Fatto" {
		t.Errorf("properties = %v", got)
	}
	// --title was not passed, so the title column is not being written.
	if _, ok := got["Name"]; ok {
		t.Errorf("plan claims it would write the title: %v", got)
	}
}

func TestThePlanCountsTheBodyBlocks(t *testing.T) {
	var seen []string
	svc := dryRunService(t, rowJSON, &seen)

	res, err := svc.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231"},
		&BodyRequest{Blocks: make([]notion.Block, 3)})
	if err != nil {
		t.Fatal(err)
	}

	if res.Plan.BodyBlocks != 3 {
		t.Errorf("body blocks = %d, want 3", res.Plan.BodyBlocks)
	}
	// And the body endpoints were never touched.
	for _, req := range seen {
		if strings.Contains(req, "/children") || strings.HasPrefix(req, "DELETE") {
			t.Errorf("a dry run reached a block endpoint: %s", req)
		}
	}
}

// Off by default: nothing about the ordinary path changes.
func TestWritesStillHappenWithoutDryRun(t *testing.T) {
	var seen []string
	srv := routes(t, rowJSON, &seen)
	t.Cleanup(srv.Close)
	svc := New(notion.New("ntn_test", notion.WithBaseURL(srv.URL)), testProfile())

	res, err := svc.Upsert(context.Background(),
		tracker.Fields{Ticket: "BDF-231", Status: "Fatto"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if res.Plan != nil {
		t.Error("a real run produced a plan")
	}
	if writes := wrote(seen); len(writes) == 0 {
		t.Fatalf("nothing was written: %v", seen)
	}
}
