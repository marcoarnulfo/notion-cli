package cli

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

// printJSON writes v as indented JSON. The shape of what callers pass in is a
// documented, stable scripting contract: changing a key is a breaking change.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// exitCodeFor maps an error onto the process exit code, so that pipelines can
// tell "not found" from "token expired" without parsing messages.
//
// Precedence is deliberate: a domain error wins over an explicit codedError
// wrapping it, because the domain type describes what actually went wrong more
// precisely than any code a caller attaches on the way out. Anyone wrapping a
// domain error with %w and a different code should expect the domain code to
// win — use %v to state a code that must survive.
func exitCodeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	var (
		dup     *tracker.DuplicateError
		invalid *tracker.ValidationError
	)
	switch {
	case errors.As(err, &dup):
		return ExitDuplicate
	case errors.As(err, &invalid):
		return ExitUsage
	case errors.Is(err, service.ErrNotFound), errors.Is(err, notion.ErrNotFound):
		return ExitNotFound
	// Not yet configured is the same class of mistake as a missing flag: the
	// invocation cannot work as written, and the fix is the user's to make.
	case errors.Is(err, config.ErrNotConfigured):
		return ExitUsage
	// --ticket "" is a missing value wearing a passed flag; cobra's
	// MarkFlagRequired cannot catch it, so service.Upsert/Set/Get do.
	case errors.Is(err, service.ErrEmptyTicket):
		return ExitUsage
	case errors.Is(err, notion.ErrUnauthorized):
		return ExitAuth
	}
	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	// Cobra reports missing required flags with this exact prefix and no typed
	// error to match on.
	if strings.HasPrefix(err.Error(), `required flag(s) `) {
		return ExitUsage
	}
	return ExitError
}
