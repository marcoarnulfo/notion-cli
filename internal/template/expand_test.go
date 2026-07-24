package template

import (
	"errors"
	"strings"
	"testing"
)

func vars() map[string]string {
	return map[string]string{"ticket": "BDF-231", "date": "2026-07-24"}
}

func TestExpand(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"a placeholder on its own", "{{ticket}}", "BDF-231"},
		{"inside a sentence", "Fixed in {{ticket}} today.", "Fixed in BDF-231 today."},
		{"both known names", "{{ticket}} — {{date}}", "BDF-231 — 2026-07-24"},
		{"repeated", "{{ticket}} {{ticket}}", "BDF-231 BDF-231"},
		{"whitespace inside the braces", "{{ ticket }}", "BDF-231"},
		{"across lines", "# {{ticket}}\n\nclosed on {{date}}\n", "# BDF-231\n\nclosed on 2026-07-24\n"},
		{"nothing to expand", "plain body\n", "plain body\n"},
		// A single brace is not a placeholder, and neither is an unclosed one:
		// a body full of JSON or CSS must survive expansion untouched.
		{"single braces", "{ticket} and { ticket }", "{ticket} and { ticket }"},
		{"unclosed", "{{ticket", "{{ticket"},
		{"json body", `{"a": 1}`, `{"a": 1}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Expand(tc.src, vars())
			if err != nil {
				t.Fatalf("Expand: %v", err)
			}
			if got != tc.want {
				t.Errorf("Expand(%q) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

// The whole point of failing: a body reaching Notion with a literal
// {{tikcet}} in it is a typo nobody notices until they read the page.
func TestExpandRejectsAnUnknownPlaceholder(t *testing.T) {
	_, err := Expand("# Title\n\nsee {{tikcet}}\n", vars())

	var unknown *UnknownPlaceholderError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want an UnknownPlaceholderError", err)
	}
	if unknown.Name != "tikcet" {
		t.Errorf("name = %q", unknown.Name)
	}
	if unknown.Line != 3 {
		t.Errorf("line = %d, want 3", unknown.Line)
	}
	// The message has to be enough to fix the typo without reading the docs.
	for _, want := range []string{"tikcet", "line 3", "date, ticket"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q is missing %q", err, want)
		}
	}
}

func TestExpandRejectsAnEmptyPlaceholder(t *testing.T) {
	if _, err := Expand("a {{}} b", vars()); err == nil {
		t.Fatal("{{}} was accepted")
	}
}

// The first error is the one whose line the user will go and look at.
func TestExpandReportsTheFirstUnknownPlaceholder(t *testing.T) {
	_, err := Expand("{{one}}\n{{two}}\n", vars())

	var unknown *UnknownPlaceholderError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v", err)
	}
	if unknown.Name != "one" {
		t.Errorf("name = %q, want the first one", unknown.Name)
	}
}

// An empty value is a body saying "there is nothing here" — a ticket with no
// due date — not a mistake to reject.
func TestExpandSubstitutesAnEmptyValue(t *testing.T) {
	got, err := Expand("due: {{due}}", map[string]string{"due": ""})
	if err != nil {
		t.Fatal(err)
	}
	if got != "due: " {
		t.Errorf("got %q", got)
	}
}

// Expansion happens once over the source: a value that itself looks like a
// placeholder is data, not a further instruction.
func TestExpandDoesNotRecurse(t *testing.T) {
	got, err := Expand("{{ticket}}", map[string]string{"ticket": "{{date}}", "date": "no"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "{{date}}" {
		t.Errorf("got %q, want the substituted value left alone", got)
	}
}
