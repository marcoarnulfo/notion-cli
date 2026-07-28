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

// ticketIsTitle reports whether the profile maps the ticket key and the title
// onto the same column, as a board keyed by task name does. The human-readable
// output of list and get then has one value to show, not two, and printing both
// roles side by side just repeats it.
//
// The empty check is not redundant: a hand-written config that maps neither
// role leaves both names empty and equal, and that is a broken mapping for
// doctor to report — not two roles deliberately sharing one column.
//
// --json deliberately keeps both keys even when they carry the same value: its
// shape is a stable contract, and a script that reads .ticket must keep working
// whatever the board is keyed by.
func ticketIsTitle(props config.Properties) bool {
	return props.Ticket != "" && props.Ticket == props.Title
}

// assigneeSuffix formats a row's assignee for human-readable output: a
// leading two-space column separator and an "@" so it reads as a mention, or
// the empty string when there is nothing to show. get and list both append
// this to the same row, so the separator is a shared presentation rule like
// ticketIsTitle's merge, not something to inline twice and risk drifting
// apart between the two commands.
func assigneeSuffix(p notion.Page, props config.Properties) string {
	if name := p.Properties[props.Assignee].Text; name != "" {
		return "  @" + name
	}
	return ""
}

// prioritySuffix is assigneeSuffix's twin, and exists for the same reason: the
// two-space separator and the sigil must not drift between get and list.
//
// A different sigil from the assignee's "@" on purpose — two marks for two
// different things, readable in a row that carries both and has no header.
func prioritySuffix(p notion.Page, props config.Properties) string {
	if value := p.Properties[props.Priority].Text; value != "" {
		return "  !" + value
	}
	return ""
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
	// A body write that failed after properties were applied is a partial
	// success: exit 1 regardless of the underlying status, so a wrapped 400
	// does not masquerade as a usage error.
	var bodyErr *service.BodyWriteError
	if errors.As(err, &bodyErr) {
		return ExitError
	}
	var (
		dup       *tracker.DuplicateError
		invalid   *tracker.ValidationError
		ambiguous *tracker.AmbiguousOptionError
		apiErr    *notion.APIError
	)
	switch {
	case errors.As(err, &dup):
		return ExitDuplicate
	case errors.As(err, &invalid):
		return ExitUsage
	case errors.As(err, &ambiguous):
		return ExitUsage
	// Every way of getting the assignee wrong is a mistake the user can fix by
	// rewriting the command, which is exactly what exit code 2 means.
	case errors.Is(err, service.ErrEmptyAssignee),
		errors.Is(err, service.ErrNoIdentity),
		errors.Is(err, service.ErrConflictingListFilter),
		errors.Is(err, tracker.ErrConflictingAssignee):
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
