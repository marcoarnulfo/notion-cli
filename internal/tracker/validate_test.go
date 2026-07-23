package tracker

import (
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
