package tracker

import (
	"fmt"
	"strings"
)

// AmbiguousOptionError marks a value that matched more than one option.
//
// It is a distinct type from ValidationError because the two failures deserve
// different fixes: an unknown value needs the list of what exists, an ambiguous
// one needs more characters of a name the user already got right.
type AmbiguousOptionError struct {
	Field   string
	Value   string
	Matches []string
}

func (e *AmbiguousOptionError) Error() string {
	return fmt.Sprintf("ambiguous %s %q: matches %s\n  fix: pass more of the name",
		e.Field, e.Value, strings.Join(e.Matches, ", "))
}

// ResolveOption turns a value the user typed into the exact option the data
// source carries, so that "mirko" reaches Notion as "Mirko Spinato".
//
// Three passes, each stricter than useful on its own, tried in order and
// stopping at the first that yields exactly one candidate: exact, exact
// case-insensitive, then substring case-insensitive. The order is what makes an
// option that is a substring of another one still reachable — without it,
// "Marco" could never be selected on a board that also has "Marco Arnulfo".
//
// Ambiguity is never resolved by picking one: on a column of people's names,
// guessing wrong assigns someone else's work to someone, and a second word of
// the name costs the user nothing.
func ResolveOption(field, query string, options []string) (string, error) {
	// An empty query would match every option as a substring. Everywhere else
	// in notion-track an empty value means "leave this alone" and never reaches
	// a resolver at all; reaching one is a caller's bug, and answering it with
	// an arbitrary option would be the worst possible recovery.
	if query == "" {
		return "", &ValidationError{Field: field, Value: query, Allowed: options}
	}

	for _, match := range []func(option string) bool{
		func(option string) bool { return option == query },
		func(option string) bool { return strings.EqualFold(option, query) },
		func(option string) bool {
			return strings.Contains(strings.ToLower(option), strings.ToLower(query))
		},
	} {
		var found []string
		for _, option := range options {
			if match(option) {
				found = append(found, option)
			}
		}
		switch len(found) {
		case 1:
			return found[0], nil
		case 0:
			continue // a looser pass may still find it
		default:
			return "", &AmbiguousOptionError{Field: field, Value: query, Matches: found}
		}
	}
	return "", &ValidationError{Field: field, Value: query, Allowed: options}
}
