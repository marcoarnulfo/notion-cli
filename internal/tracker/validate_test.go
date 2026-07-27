package tracker

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateStatusAcceptsAKnownOption(t *testing.T) {
	if err := ValidateStatus("Fatto", []string{"Backlog", "In corso", "Fatto"}); err != nil {
		t.Fatalf("ValidateStatus: %v", err)
	}
}

func TestValidateStatusRejectsUnknownAndListsTheOptions(t *testing.T) {
	err := ValidateStatus("Fattto", []string{"Backlog", "In corso", "Fatto"})
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"Fattto", "Backlog", "In corso", "Fatto"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message is missing %q: %s", want, msg)
		}
	}
}

func TestValidateStatusIsCaseSensitive(t *testing.T) {
	// Notion option names are case sensitive; accepting "fatto" here would
	// create a second option on a select property.
	if err := ValidateStatus("fatto", []string{"Fatto"}); err == nil {
		t.Fatal("expected case mismatch to be rejected")
	}
}

func TestValidateStatusSkipsWhenNoOptionsAreKnown(t *testing.T) {
	// An empty allow-list means the schema could not be read; refusing every
	// value would be worse than letting the API have the final say.
	if err := ValidateStatus("anything", nil); err != nil {
		t.Fatalf("ValidateStatus: %v", err)
	}
}

func TestValidateOptionNamesTheField(t *testing.T) {
	err := ValidateOption("assignee", "Marko", []string{"Marco Arnulfo"})

	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *ValidationError", err)
	}
	if invalid.Field != "assignee" {
		t.Errorf("Field = %q, want %q", invalid.Field, "assignee")
	}
}

func TestValidateStatusStillSaysStatus(t *testing.T) {
	// The wrapper exists so that every existing caller and every existing
	// message stays exactly as it was.
	err := ValidateStatus("Nope", []string{"Da fare"})

	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *ValidationError", err)
	}
	if invalid.Field != "status" {
		t.Errorf("Field = %q, want %q", invalid.Field, "status")
	}
}
