package tracker

import (
	"fmt"
	"slices"
	"strings"
)

// ValidationError marks a value the user supplied as unusable. Callers map it
// onto the "invalid usage" exit code, so it must not be used for failures the
// user could not have avoided.
type ValidationError struct {
	Field   string
	Value   string
	Allowed []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("unknown %s %q; allowed values are: %s",
		e.Field, e.Value, strings.Join(e.Allowed, ", "))
}

// ValidateOption checks a value against the options read from the server.
//
// This matters most for select properties: Notion creates an unknown select
// option on write, so an unchecked typo becomes a permanent bogus value in the
// database. Status properties reject unknown values server-side, but failing
// here still produces a far better message than the API's.
//
// field names the role in the message, and — more importantly — travels into
// ValidationError, which is what maps this failure onto exit code 2 for callers
// that never touch cobra (apply, the MCP server).
//
// An empty allowed list disables the check.
func ValidateOption(field, value string, allowed []string) error {
	if len(allowed) == 0 || slices.Contains(allowed, value) {
		return nil
	}
	return &ValidationError{Field: field, Value: value, Allowed: allowed}
}

// ValidateStatus is ValidateOption for the status role.
func ValidateStatus(value string, allowed []string) error {
	return ValidateOption("status", value, allowed)
}
