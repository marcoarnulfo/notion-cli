package tracker

import (
	"fmt"
	"strconv"
	"strings"
)

// InvalidIDError marks an --id value that cannot be turned into the number
// Notion filters on.
//
// Callers map it onto the "invalid usage" exit code. It lives here because the
// parsing does, not because a caller outside the CLI produces it today: with
// manifests and MCP tool arguments out of scope, the CLI is the only path that
// can.
type InvalidIDError struct {
	// Value is what the user typed, verbatim: the message quotes it so they can
	// compare it against what they meant to type.
	Value string
	// Reason is the clause after the colon in Error(). It already carries the
	// column's prefix wherever the prefix is what went wrong, which is why
	// there is no separate Prefix field for a caller nobody has yet.
	Reason string
}

func (e *InvalidIDError) Error() string {
	return fmt.Sprintf("invalid id %q: %s", e.Value, e.Reason)
}

// isASCIIDigits reports whether s is one or more ASCII digits.
//
// Explicitly ASCII, never unicode.IsDigit: the latter accepts Arabic-Indic
// digits and dozens of other numeral systems that strconv.ParseInt then
// rejects, which would turn a precise "this is not a number" into a generic
// parse failure — for input a copy-paste can produce without anyone meaning to.
func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ParseUniqueID turns what the user typed into the number Notion filters on.
//
// Both "BDF-271" and "271" are accepted: the first is what the board shows, the
// second is what the API wants, and refusing either would be a rule to
// remember for no gain.
//
// prefix is the column's own prefix, read from the schema. It is what makes
// "ABC-271 belongs to another board" a message we can produce before any
// request is sent, rather than a query that quietly comes back empty.
func ParseUniqueID(input, prefix string) (int64, error) {
	trimmed := strings.TrimSpace(input)

	bad := func(reason string) (int64, error) {
		return 0, &InvalidIDError{Value: input, Reason: reason}
	}
	malformed := func() (int64, error) {
		if prefix != "" {
			return bad(fmt.Sprintf("expected a number, optionally prefixed (e.g. %q or %q)",
				prefix+"-1", "1"))
		}
		return bad(`expected a number (e.g. "1")`)
	}

	digits := trimmed
	if !isASCIIDigits(digits) {
		// Split at the last "-", not the first: a prefix containing one would
		// otherwise take the number half with it.
		i := strings.LastIndex(trimmed, "-")
		// i <= 0 covers both "no dash at all" and a leading dash, which is an
		// empty prefix half rather than a prefixless id — "-271" is a typo, not
		// a valid way to spell 271.
		if i <= 0 || i == len(trimmed)-1 {
			return malformed()
		}
		given, rest := trimmed[:i], trimmed[i+1:]
		if !isASCIIDigits(rest) {
			return malformed()
		}
		if prefix == "" {
			return bad("this board's ids have no prefix, so a bare number is expected")
		}
		if !strings.EqualFold(given, prefix) {
			return bad(fmt.Sprintf("this board's ids start with %q", prefix))
		}
		digits = rest
	}

	// isASCIIDigits has already ruled out everything but length: ParseInt can
	// still fail here on a number too large for int64.
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return malformed()
	}
	if n < 1 {
		return bad("ids start at 1")
	}
	return n, nil
}
