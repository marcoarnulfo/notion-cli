package manifest

import (
	"strings"
	"testing"
)

func TestParseJSON(t *testing.T) {
	entries, err := Parse("m.json", []byte(`[
		{"op":"upsert","ticket":"BDF-1","title":"Hardening","status":"In corso","due":"2026-08-01"},
		{"op":"set","ticket":"BDF-2","status":"Fatto"}
	]`))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("%d entries, want 2", len(entries))
	}
	first := entries[0]
	if first.Op != OpUpsert || first.Ticket != "BDF-1" || first.Title != "Hardening" ||
		first.Status != "In corso" || first.Due != "2026-08-01" || first.Index != 1 {
		t.Errorf("first = %+v", first)
	}
	if entries[1].Op != OpSet || entries[1].Index != 2 {
		t.Errorf("second = %+v", entries[1])
	}
}

func TestParseCSV(t *testing.T) {
	entries, err := Parse("m.csv", []byte(
		"op,ticket,title,status,due,body_file\n"+
			"upsert,BDF-1,Hardening,In corso,2026-08-01,notes.md\n"+
			"set,BDF-2,,Fatto,,\n"))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("%d entries, want 2", len(entries))
	}
	if entries[0].BodyFile != "notes.md" || entries[0].Status != "In corso" {
		t.Errorf("first = %+v", entries[0])
	}
	// An empty CSV cell is "not set", the same as an absent JSON key.
	if entries[1].Title != "" || entries[1].Due != "" || entries[1].Op != OpSet {
		t.Errorf("second = %+v", entries[1])
	}
}

// Defaulting to the idempotent operation means a manifest run twice by
// mistake leaves the board as it was.
func TestAnEntryWithNoOpIsAnUpsert(t *testing.T) {
	for _, tc := range []struct{ name, path, data string }{
		{"json", "m.json", `[{"ticket":"BDF-1"}]`},
		{"csv", "m.csv", "ticket\nBDF-1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := Parse(tc.path, []byte(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			if entries[0].Op != OpUpsert {
				t.Errorf("op = %q, want upsert", entries[0].Op)
			}
		})
	}
}

// Silently ignoring an unknown field is how "stuats" ends up leaving every
// row's status unset with nothing said about it.
func TestParseRejectsAnUnknownField(t *testing.T) {
	for _, tc := range []struct{ name, path, data string }{
		{"json", "m.json", `[{"ticket":"BDF-1","stuats":"Fatto"}]`},
		{"csv", "m.csv", "ticket,stuats\nBDF-1,Fatto\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.path, []byte(tc.data))
			if err == nil {
				t.Fatal("an unknown field was accepted")
			}
			if !strings.Contains(err.Error(), "stuats") {
				t.Errorf("error = %q, want it to name the field", err)
			}
			// And to say what the right ones are.
			if !strings.Contains(err.Error(), "status") {
				t.Errorf("error = %q, want it to list the known fields", err)
			}
		})
	}
}

func TestParseRejectsAnUnknownOp(t *testing.T) {
	_, err := Parse("m.json", []byte(`[{"op":"delete","ticket":"BDF-1"}]`))

	if err == nil {
		t.Fatal("an unknown op was accepted")
	}
	if !strings.Contains(err.Error(), "delete") {
		t.Errorf("error = %q", err)
	}
}

func TestParseRequiresATicket(t *testing.T) {
	for _, tc := range []struct{ name, path, data string }{
		{"json", "m.json", `[{"status":"Fatto"}]`},
		{"csv", "m.csv", "status\nFatto\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.path, []byte(tc.data)); err == nil {
				t.Fatal("an entry with no ticket was accepted")
			}
		})
	}
}

// The error has to point at a line the user can go and look at.
func TestParseNamesTheOffendingEntry(t *testing.T) {
	_, err := Parse("m.json", []byte(`[{"ticket":"BDF-1"},{"ticket":"BDF-2","op":"nope"}]`))

	if err == nil || !strings.Contains(err.Error(), "entry 2") {
		t.Fatalf("error = %v, want it to name entry 2", err)
	}
}

func TestParseRejectsAnUnknownExtension(t *testing.T) {
	_, err := Parse("m.yaml", []byte("- ticket: BDF-1\n"))

	if err == nil {
		t.Fatal("a .yaml manifest was accepted")
	}
	for _, want := range []string{".json", ".csv"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %s", err, want)
		}
	}
}

func TestParseRejectsMalformedInput(t *testing.T) {
	if _, err := Parse("m.json", []byte(`{"ticket":"BDF-1"}`)); err == nil {
		t.Error("a JSON object was accepted where a list is required")
	}
	if _, err := Parse("m.csv", []byte("ticket\nBDF-1,extra\n")); err == nil {
		t.Error("a ragged CSV row was accepted")
	}
}

// A non-string value is a mistake worth naming: {"ticket": 231} would
// otherwise have to be coerced, and guessing at the user's intent in a file
// that drives writes is not worth the convenience.
func TestParseRejectsANonStringValue(t *testing.T) {
	_, err := Parse("m.json", []byte(`[{"ticket":231}]`))

	if err == nil || !strings.Contains(err.Error(), "ticket") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseAcceptsAnEmptyManifest(t *testing.T) {
	entries, err := Parse("m.json", []byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%d entries, want none", len(entries))
	}
}
