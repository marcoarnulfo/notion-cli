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
		apiErr  *notion.APIError
	)
	switch {
	case errors.As(err, &dup):
		return ExitDuplicate
	case errors.As(err, &invalid):
		return ExitUsage
	// A 400 from Notion is, by construction, a value the API rejected (e.g.
	// an unparseable date passed to --due) — invalid usage, same as
	// tracker.ValidationError, not the network/API catch-all every other
	// APIError status falls into below.
	case errors.As(err, &apiErr) && apiErr.Status == 400:
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
	// --page-id "" is the same shape of mistake as an empty --ticket, and a
	// malformed one (any input NormalizePageID could not recognize) is a
	// usage error caught before any request is even made.
	case errors.Is(err, service.ErrEmptyPageID), errors.Is(err, notion.ErrMalformedPageID):
		return ExitUsage
	// A page addressed by id that resolves but belongs to another data
	// source is a usage mistake (the wrong id, or the wrong --profile), not
	// a network or auth failure.
	case errors.Is(err, service.ErrPageOutsideProfile):
		return ExitUsage
	case errors.Is(err, notion.ErrUnauthorized):
		return ExitAuth
	// A credentials.yml nobody can read is an authentication failure like
	// any other: there may be a token in there, but nothing can prove it,
	// which is exactly the situation ExitAuth already describes for every
	// other command. A credentials.yml that can be read but fails to parse
	// is the same problem by the same reasoning — there may still be a
	// token in there, just not one anything can prove exists — so it must
	// exit the same way rather than falling through to the generic
	// ExitError below.
	case errors.Is(err, config.ErrCredentialsUnreadable), errors.Is(err, config.ErrInvalidCredentials):
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
	// MarkFlagsMutuallyExclusive/MarkFlagsOneRequired (--ticket vs --page-id)
	// fail validation with a plain fmt.Errorf, no typed error either; every
	// message cobra's flag_groups.go produces contains this exact phrase.
	if strings.Contains(err.Error(), "the group [") {
		return ExitUsage
	}
	return ExitError
}
