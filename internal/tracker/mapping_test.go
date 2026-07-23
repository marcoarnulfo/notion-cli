package tracker

import (
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

func TestGuessMappingPicksTheObviousCandidates(t *testing.T) {
	got := GuessMapping(testSchema())
	if got.Title != "Name" {
		t.Errorf("title = %q, want Name", got.Title)
	}
	if got.Ticket != "Ticket" {
		t.Errorf("ticket = %q, want Ticket", got.Ticket)
	}
	if got.Status != "Stato" {
		t.Errorf("status = %q, want Stato", got.Status)
	}
	if got.Due != "Scadenza" {
		t.Errorf("due = %q, want Scadenza", got.Due)
	}
}

func TestGuessMappingRecognisesEnglishNames(t *testing.T) {
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Title":    {Name: "Title", Type: "title"},
		"Key":      {Name: "Key", Type: "rich_text"},
		"Status":   {Name: "Status", Type: "status"},
		"Due date": {Name: "Due date", Type: "date"},
	}}
	got := GuessMapping(schema)
	if got.Ticket != "Key" || got.Status != "Status" || got.Due != "Due date" {
		t.Fatalf("mapping = %+v", got)
	}
}

func TestGuessMappingFallsBackToTheOnlyCandidateOfAType(t *testing.T) {
	// No recognisable name, but a single status column: it can only be that.
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Titolo": {Name: "Titolo", Type: "title"},
		"Fase":   {Name: "Fase", Type: "status"},
		"Codice": {Name: "Codice", Type: "rich_text"},
	}}
	got := GuessMapping(schema)
	if got.Title != "Titolo" || got.Status != "Fase" || got.Ticket != "Codice" {
		t.Fatalf("mapping = %+v", got)
	}
}

func TestGuessMappingLeavesAmbiguityToTheUser(t *testing.T) {
	// Two unnamed rich_text columns: guessing would be a coin flip.
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Titolo": {Name: "Titolo", Type: "title"},
		"Alfa":   {Name: "Alfa", Type: "rich_text"},
		"Beta":   {Name: "Beta", Type: "rich_text"},
	}}
	if got := GuessMapping(schema); got.Ticket != "" {
		t.Fatalf("ticket = %q, want an empty guess", got.Ticket)
	}
}
