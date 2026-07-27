package tracker

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveOption(t *testing.T) {
	options := []string{"Andrea Ghidara", "Marco Arnulfo", "Mirko Spinato"}

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"exact match", "Mirko Spinato", "Mirko Spinato"},
		{"case-insensitive exact match", "mirko spinato", "Mirko Spinato"},
		{"unique prefix", "mirko", "Mirko Spinato"},
		{"unique substring anywhere", "spinato", "Mirko Spinato"},
		{"substring in the middle of a word", "ghid", "Andrea Ghidara"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveOption("assignee", tt.query, options)
			if err != nil {
				t.Fatalf("ResolveOption(%q): %v", tt.query, err)
			}
			if got != tt.want {
				t.Errorf("ResolveOption(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestResolveOptionExactBeatsSubstring(t *testing.T) {
	// "Marco" is an option in its own right AND a substring of another one.
	// The exact match must win, or naming an option exactly would be
	// impossible whenever a longer option contains it.
	options := []string{"Marco", "Marco Arnulfo"}
	got, err := ResolveOption("assignee", "Marco", options)
	if err != nil {
		t.Fatalf("ResolveOption: %v", err)
	}
	if got != "Marco" {
		t.Errorf("ResolveOption = %q, want the exact option %q", got, "Marco")
	}
}

func TestResolveOptionAmbiguous(t *testing.T) {
	options := []string{"Andrea Ghidara", "Marco Arnulfo", "Mirko Spinato"}

	_, err := ResolveOption("assignee", "ar", options)
	var ambiguous *AmbiguousOptionError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("error = %v, want *AmbiguousOptionError", err)
	}
	if len(ambiguous.Matches) != 2 {
		t.Fatalf("Matches = %v, want the two names containing \"ar\"", ambiguous.Matches)
	}
	// The message must name every candidate: telling the user it is ambiguous
	// without saying between what leaves them guessing.
	for _, want := range []string{"Andrea Ghidara", "Marco Arnulfo"} {
		if !strings.Contains(ambiguous.Error(), want) {
			t.Errorf("Error() = %q, want it to name %q", ambiguous.Error(), want)
		}
	}
}

func TestResolveOptionUnknown(t *testing.T) {
	options := []string{"Andrea Ghidara", "Marco Arnulfo", "Mirko Spinato"}

	_, err := ResolveOption("assignee", "Marko", options)
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *ValidationError so it exits 2", err)
	}
	if invalid.Field != "assignee" {
		t.Errorf("Field = %q, want %q", invalid.Field, "assignee")
	}
}

func TestResolveOptionEdgeCases(t *testing.T) {
	options := []string{"Andrea Ghidara"}

	t.Run("an empty query is not a match-anything wildcard", func(t *testing.T) {
		if _, err := ResolveOption("assignee", "", options); err == nil {
			t.Fatal("ResolveOption(\"\") = nil error, want a failure")
		}
	})

	t.Run("no options at all", func(t *testing.T) {
		if _, err := ResolveOption("assignee", "Andrea", nil); err == nil {
			t.Fatal("ResolveOption with no options = nil error, want a failure")
		}
	})
}
