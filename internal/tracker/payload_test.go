package tracker

import (
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

func testSchema() *notion.Schema {
	return &notion.Schema{
		DataSourceID: "ds1",
		Properties: map[string]notion.Property{
			"Name":     {Name: "Name", Type: "title"},
			"Ticket":   {Name: "Ticket", Type: "rich_text"},
			"Stato":    {Name: "Stato", Type: "status", Options: []string{"Backlog", "In corso", "Fatto"}},
			"Scadenza": {Name: "Scadenza", Type: "date"},
		},
	}
}

func testProps() config.Properties {
	return config.Properties{Ticket: "Ticket", Status: "Stato", Title: "Name", Due: "Scadenza"}
}

func TestBuildPropertiesOnlyIncludesProvidedFields(t *testing.T) {
	got, err := BuildProperties(Fields{Ticket: "BDF-231"}, testProps(), testSchema())
	if err != nil {
		t.Fatalf("BuildProperties: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d properties, want only Ticket: %v", len(got), got)
	}
	if got["Ticket"] == nil {
		t.Fatal("Ticket is missing")
	}
}

func TestBuildPropertiesShapesEachType(t *testing.T) {
	got, err := BuildProperties(Fields{
		Ticket: "BDF-231",
		Title:  "Hardening",
		Status: "Fatto",
		Due:    "2026-08-01",
	}, testProps(), testSchema())
	if err != nil {
		t.Fatalf("BuildProperties: %v", err)
	}

	ticket := got["Ticket"].(map[string]any)["rich_text"].([]map[string]any)
	if ticket[0]["text"].(map[string]string)["content"] != "BDF-231" {
		t.Errorf("Ticket payload = %v", got["Ticket"])
	}
	title := got["Name"].(map[string]any)["title"].([]map[string]any)
	if title[0]["text"].(map[string]string)["content"] != "Hardening" {
		t.Errorf("Name payload = %v", got["Name"])
	}
	if got["Stato"].(map[string]any)["status"].(map[string]string)["name"] != "Fatto" {
		t.Errorf("Stato payload = %v", got["Stato"])
	}
	if got["Scadenza"].(map[string]any)["date"].(map[string]string)["start"] != "2026-08-01" {
		t.Errorf("Scadenza payload = %v", got["Scadenza"])
	}
}

func TestBuildPropertiesUsesSelectShapeForSelectStatus(t *testing.T) {
	schema := testSchema()
	schema.Properties["Stato"] = notion.Property{
		Name: "Stato", Type: "select", Options: []string{"Fatto"},
	}
	got, err := BuildProperties(Fields{Status: "Fatto"}, testProps(), schema)
	if err != nil {
		t.Fatalf("BuildProperties: %v", err)
	}
	if got["Stato"].(map[string]any)["select"].(map[string]string)["name"] != "Fatto" {
		t.Errorf("Stato payload = %v", got["Stato"])
	}
}

func TestBuildPropertiesRejectsUnknownStatus(t *testing.T) {
	_, err := BuildProperties(Fields{Status: "Fattto"}, testProps(), testSchema())
	if err == nil {
		t.Fatal("expected an unknown status to be rejected")
	}
}

func TestBuildPropertiesRejectsAMappingThatNoLongerMatchesTheSchema(t *testing.T) {
	props := testProps()
	props.Status = "Status" // renamed in Notion, config not updated
	_, err := BuildProperties(Fields{Status: "Fatto"}, props, testSchema())
	if err == nil {
		t.Fatal("expected a missing property to be reported")
	}
}

func TestBuildPropertiesTicketCanBeTheTitle(t *testing.T) {
	// Some databases use the title column as the ticket key.
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Key": {Name: "Key", Type: "title"},
	}}
	props := config.Properties{Ticket: "Key", Title: "Key"}
	got, err := BuildProperties(Fields{Ticket: "BDF-231"}, props, schema)
	if err != nil {
		t.Fatalf("BuildProperties: %v", err)
	}
	title := got["Key"].(map[string]any)["title"].([]map[string]any)
	if title[0]["text"].(map[string]string)["content"] != "BDF-231" {
		t.Errorf("Key payload = %v", got["Key"])
	}
}

// The ticket must win when both land on the same column, which is only true
// because title is added first. Swapping the two add() calls leaves every other
// test in this file green, so this is the one that pins the order.
func TestBuildPropertiesTicketBeatsTitleOnASharedColumn(t *testing.T) {
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Key": {Name: "Key", Type: "title"},
	}}
	props := config.Properties{Ticket: "Key", Title: "Key"}

	got, err := BuildProperties(Fields{Ticket: "BDF-231", Title: "Some other title"}, props, schema)
	if err != nil {
		t.Fatalf("BuildProperties: %v", err)
	}
	title := got["Key"].(map[string]any)["title"].([]map[string]any)
	if content := title[0]["text"].(map[string]string)["content"]; content != "BDF-231" {
		t.Fatalf("content = %q, want the ticket key to win", content)
	}
}

// init does not require --due-prop, so an unmapped due role is the normal
// configuration, not an edge case. Silently dropping an explicit --due
// value there used to make `upsert --due 2026-01-01` exit 0 without ever
// sending the date — the exact same shape of data loss that a property
// mapped to a nonexistent schema column already refuses to allow (see
// TestBuildPropertiesRejectsAMappingThatNoLongerMatchesTheSchema below).
func TestBuildPropertiesRejectsAnExplicitValueForAnUnmappedProperty(t *testing.T) {
	props := testProps()
	props.Due = "" // due left unconfigured, same as init leaves it by default
	_, err := BuildProperties(Fields{Ticket: "BDF-231", Due: "2026-01-01"}, props, testSchema())
	if err == nil {
		t.Fatal("expected an explicit due value with no mapped property to be rejected")
	}
	if !strings.Contains(err.Error(), "due-prop") {
		t.Fatalf("error does not tell the user how to map it: %v", err)
	}
}

// Leaving a field blank must still mean "don't touch this property" — that
// is what makes `set --status` a partial update — even for a role with no
// mapping at all.
func TestBuildPropertiesStillAllowsAnEmptyValueOnAnUnmappedProperty(t *testing.T) {
	props := testProps()
	props.Due = ""
	got, err := BuildProperties(Fields{Ticket: "BDF-231"}, props, testSchema())
	if err != nil {
		t.Fatalf("BuildProperties: %v", err)
	}
	if _, ok := got["Scadenza"]; ok {
		t.Fatalf("an empty due value must not add a Scadenza property: %v", got)
	}
}

func TestBuildPropertiesRejectsAnUnsupportedPropertyType(t *testing.T) {
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Name":  {Name: "Name", Type: "title"},
		"Count": {Name: "Count", Type: "number"},
	}}
	props := config.Properties{Title: "Name", Due: "Count"}

	if _, err := BuildProperties(Fields{Due: "3"}, props, schema); err == nil {
		t.Fatal("expected an unsupported property type to be rejected")
	}
}
