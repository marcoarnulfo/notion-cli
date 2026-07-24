// Package template expands the placeholders notion-track understands in a
// Markdown body, before it is parsed into blocks.
//
// It is deliberately not text/template. That package brings conditionals,
// loops, function calls and a syntax users must learn; a task body needs the
// ticket key and today's date substituted in. Everything else it would offer
// is a way for a body to fail in a way its author cannot debug from a CLI
// error message.
package template

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// placeholderPattern matches {{name}}, tolerating whitespace inside the
// braces, and captures the name.
var placeholderPattern = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)

// UnknownPlaceholderError names a placeholder nothing can fill in, and where
// it is.
//
// Expansion fails on it rather than leaving it in place: a body reaching
// Notion with a literal {{tikcet}} in it is a typo nobody notices until they
// read the page, while an error names the line before anything is written.
type UnknownPlaceholderError struct {
	Name  string
	Line  int
	Known []string
}

func (e *UnknownPlaceholderError) Error() string {
	return fmt.Sprintf("unknown placeholder {{%s}} on line %d; known placeholders: %s",
		e.Name, e.Line, strings.Join(e.Known, ", "))
}

// Expand replaces every {{name}} in src with vars[name].
//
// A name with no value in vars is an error, and so is an empty {{}}. A value
// that is itself empty — a ticket with no due date, say — substitutes as the
// empty string: that is a body saying "there is nothing here", not a mistake.
//
// There is no escape for a literal {{: expansion is opt-in per invocation, so
// a body that needs braces simply does not ask for it.
func Expand(src string, vars map[string]string) (string, error) {
	known := knownNames(vars)

	var err error
	out := placeholderPattern.ReplaceAllStringFunc(src, func(match string) string {
		if err != nil {
			// ReplaceAllStringFunc has no way to stop early; keep the first
			// error, which is the one whose line number the user will look at.
			return match
		}
		name := placeholderPattern.FindStringSubmatch(match)[1]
		value, ok := vars[name]
		if !ok {
			err = &UnknownPlaceholderError{
				Name:  name,
				Line:  lineOf(src, match),
				Known: known,
			}
			return match
		}
		return value
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

// lineOf is the 1-based line the first occurrence of match sits on.
func lineOf(src, match string) int {
	idx := strings.Index(src, match)
	if idx < 0 {
		return 1
	}
	return strings.Count(src[:idx], "\n") + 1
}

func knownNames(vars map[string]string) []string {
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names) // a map's order would reshuffle the error message per run
	return names
}
