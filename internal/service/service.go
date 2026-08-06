// Package service orchestrates the client, the config and the domain.
//
// It is the only layer where those three meet, which is what lets the CLI, the
// TUI and (later) the MCP adapter share one implementation of every operation.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

// ErrNotFound means no row carries the requested ticket key.
var ErrNotFound = errors.New("ticket not found")

// ErrEmptyTicket means a ticket key was supplied but is blank.
//
// cobra's MarkFlagRequired only checks that a flag was passed, not that it
// carries a value, so `--ticket ""` reaches here unless it is rejected
// explicitly. Left unchecked, BuildProperties treats the empty value as
// "leave this property alone" (the same rule that makes `set --status` a
// partial update) and silently omits the ticket property from the payload —
// upsert then creates a row no future get/set/upsert can ever find again.
// Checked once here rather than in each CLI command so the TUI and any
// future MCP adapter inherit the guard for free.
var ErrEmptyTicket = errors.New("ticket key must not be empty")

// ErrEmptyPageID mirrors ErrEmptyTicket for the page-id address path: a
// `--page-id ""` reaches here the same way `--ticket ""` reaches
// ErrEmptyTicket (cobra's flag-group validation only checks that the flag
// was passed, not that it carries a value), and it deserves the same clear
// "missing value" message rather than falling through to
// NormalizePageID's "malformed" one.
var ErrEmptyPageID = errors.New("page id must not be empty")

// ErrPageOutsideProfile means a page addressed by id resolved, but belongs
// to a data source other than the active profile's.
//
// GET /v1/pages/{id} succeeds for any page shared with the integration, not
// only rows of the configured data source, so without this check a `set
// --page-id` on a foreign page would sail through to UpdatePage and fail
// there with a cryptic 400 from Notion about property names that don't
// exist on that page's actual data source.
var ErrPageOutsideProfile = errors.New("page belongs to a different data source than the active profile")

// ErrNoIdentity means "--assignee me" was used without anything saying who
// "me" is.
var ErrNoIdentity = errors.New(
	"--assignee me needs to know who you are\n" +
		"  fix: run 'notion-track init --me <name>' (or export NOTION_TRACK_ME=<name> to override it)")

// ErrIdentityUnreadable means "--assignee me" was used on a run whose
// credentials file could not be read, so nothing can say who "me" is.
//
// Distinct from ErrNoIdentity on purpose: an identity may well be configured
// in there, and telling that user to configure one would send them to fix
// something that is not broken. The file is what needs repairing, and until
// it is, the environment override is the way through.
var ErrIdentityUnreadable = errors.New(
	"--assignee me cannot be resolved: your credentials file could not be read\n" +
		"  fix: repair or delete it and rerun 'notion-track init --me <name>'\n" +
		"       (or export NOTION_TRACK_ME=<name> to get through this run)")

// ErrEmptyAssignee mirrors ErrEmptyTicket: cobra reports that a flag was
// passed, never that it carries a value, so `--assignee ""` would otherwise
// reach BuildProperties and be read as "leave this alone" — silently doing
// nothing in a command the user wrote specifically to change something.
var ErrEmptyAssignee = errors.New("assignee must not be empty; use --unassign to clear it")

// ErrEmptyPriority mirrors ErrEmptyAssignee for the same reason: cobra reports
// that a flag was passed, never that it carries a value, so `--priority ""`
// would otherwise reach BuildProperties and be read as "leave this alone" —
// silently doing nothing in a command the user wrote specifically to change
// something.
var ErrEmptyPriority = errors.New("priority must not be empty")

// ErrEmptyID mirrors ErrEmptyTicket and ErrEmptyPageID for the third way of
// addressing a row: cobra reports that a flag was passed, never that it carries
// a value, so `--id ""` would otherwise reach ParseUniqueID and surface as a
// malformed id rather than a missing one.
var ErrEmptyID = errors.New("id must not be empty")

// ErrNoIDProperty means a row was addressed by board id on a profile that has
// no id column mapped. It is "not configured yet", not "broken": the role is
// optional, and a board without a unique_id column is addressed the other two
// ways.
var ErrNoIDProperty = errors.New("no id property is mapped")

// idNotFoundError reports that no row carries a board id.
//
// It exists so the message can say "id" where ErrNotFound's own text says
// "ticket": wrapping with %w prepends "ticket not found" to a failure on a
// command that never took --ticket. Unwrap keeps errors.Is(err, ErrNotFound)
// true, so the exit code stays 3.
type idNotFoundError struct{ input string }

func (e *idNotFoundError) Error() string { return "no row has id " + e.input }
func (e *idNotFoundError) Unwrap() error { return ErrNotFound }

// Service performs notion-track's operations against one profile.
//
// One Service may be shared by concurrent callers — the TUI runs its commands
// on separate goroutines — so the lazily fetched schema is guarded by a mutex.
type Service struct {
	client  *notion.Client
	profile config.Profile
	dryRun  bool

	mu     sync.Mutex
	schema *notion.Schema // read lazily, guarded by mu
}

// New builds a Service for a profile.
func New(client *notion.Client, profile config.Profile) *Service {
	return &Service{client: client, profile: profile}
}

// DryRun makes every write stop short of writing and report a Plan instead.
//
// It lives on the Service rather than on each method's signature so that the
// guard sits in one place per operation, right before the write, and cannot be
// forgotten by a future caller of Upsert or Set — including the TUI and the
// MCP adapter, which reach the same code.
func (s *Service) DryRun(on bool) *Service {
	s.dryRun = on
	return s
}

// Profile exposes the profile this service was built for, so that callers can
// map property names back onto output fields.
func (s *Service) Profile() config.Profile { return s.profile }

// Schema returns the data source schema, fetching it at most once.
// A mutex rather than sync.Once: Once would memoise a network failure forever,
// leaving the Service permanently broken after one transient error.
func (s *Service) Schema(ctx context.Context) (*notion.Schema, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.schema != nil {
		return s.schema, nil
	}
	schema, err := s.client.GetSchema(ctx, s.profile.DataSourceID)
	if err != nil {
		return nil, err
	}
	s.schema = schema
	return schema, nil
}

// Result reports what an upsert or set did.
type Result struct {
	Action string // "created" or "updated"
	Page   notion.Page
	Body   *BodyResult // non-nil only when a body was written
	// Plan is non-nil exactly when the service is in dry-run mode, and then
	// nothing was written: Page is the row as it stands now, not as it would
	// look afterwards.
	Plan *Plan
}

// BodyRequest carries an optional Markdown body to replace on a page. A nil
// *BodyRequest means "leave the page body untouched" (the pre-body behavior).
type BodyRequest struct {
	Blocks   []notion.Block
	Progress io.Writer // optional; ephemeral progress lines go here (stderr)
	// AppendMarkdown, when non-empty, appends this Markdown to the end of the
	// page instead of replacing the body: Blocks is ignored and nothing is
	// deleted. The two modes are mutually exclusive and the CLI enforces that,
	// so exactly one of Blocks / AppendMarkdown is ever set.
	AppendMarkdown string
}

// BodyResult reports what replaceBody did, for --json and stderr warnings.
type BodyResult struct {
	BlocksWritten int
	BlocksDeleted int
	Warnings      []string // e.g. skipped child_page/child_database
	// WasAppend records which mode ran, set before the call so it survives a
	// failure. Appended records whether that append actually landed. Two flags
	// rather than one because the error path has to answer both questions: a
	// single false would not distinguish "the append failed" from "this was a
	// replace", and the two want different counters reported.
	WasAppend bool
	Appended  bool
}

// BodyWriteError marks a failure that happened AFTER the row's properties were
// written (or the row created): properties are applied, only the body replace
// failed. It unwraps to the underlying error so exitCodeFor and
// errors.Is(…, notion.ErrAmbiguousWrite) keep working.
type BodyWriteError struct{ err error }

func (e *BodyWriteError) Error() string { return e.err.Error() }
func (e *BodyWriteError) Unwrap() error { return e.err }

// AmbiguousAppendError reports an append whose outcome is unknown.
//
// It exists to REPLACE notion.ErrAmbiguousWrite's own wording rather than
// prepend to it. That sentinel reads "re-run to converge", which is right for
// the replace path — re-running makes the body equal the file again — and
// exactly wrong here, where re-running an append that did land adds the
// content twice. Wrapping with %w would keep both sentences in one string,
// ending on the one that duplicates; the primary consumer of this CLI is an
// agent reading that string, so the advice it ends on is the advice it takes.
//
// Unwrap still reaches the sentinel, so errors.Is(…, notion.ErrAmbiguousWrite)
// and the exit code it drives are unchanged — only the text is ours.
type AmbiguousAppendError struct{ err error }

func (e *AmbiguousAppendError) Error() string {
	return "the append may or may not have been applied; check the page before re-running, " +
		"because re-running an append that did land adds the content twice: " + underlyingCause(e.err)
}
func (e *AmbiguousAppendError) Unwrap() error { return e.err }

// underlyingCause renders the cause without notion.ErrAmbiguousWrite's own
// sentence, so AmbiguousAppendError can state what went wrong without also
// repeating the advice it exists to override.
func underlyingCause(err error) string {
	// doRejectRetryable joins with fmt.Errorf("%w: %w", sentinel, cause), which
	// produces a MULTI-error: it implements Unwrap() []error, and errors.Unwrap
	// returns nil for it. Checking the multi form first is therefore the whole
	// point -- reaching for errors.Unwrap alone finds nothing and falls back to
	// the joined text, which is the contradictory string this exists to avoid.
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		parts := joined.Unwrap()
		// Keep whichever part is not the sentinel: the 502, the reset
		// connection -- the half that says what actually went wrong.
		var kept []string
		for _, p := range parts {
			if p != nil && !errors.Is(p, notion.ErrAmbiguousWrite) {
				kept = append(kept, p.Error())
			}
		}
		if len(kept) > 0 {
			return strings.Join(kept, ": ")
		}
	}
	if inner := errors.Unwrap(err); inner != nil && errors.Is(err, notion.ErrAmbiguousWrite) {
		return inner.Error()
	}
	return err.Error()
}

// replaceBody makes the page body equal to req.Blocks with replace semantics:
// snapshot existing children, append the new body at the end, then delete the
// snapshotted children — skipping child_page/child_database so sub-pages are
// never archived. The order converges on re-run if append fails midway. On a
// failed DELETE, res already carries the real BlocksWritten (the append DID
// happen), which the CLI surfaces even in the partial-failure JSON (spec §8).
func (s *Service) replaceBody(ctx context.Context, pageID string, req *BodyRequest) (BodyResult, error) {
	var res BodyResult
	old, err := s.client.ListBlockChildren(ctx, pageID)
	if err != nil {
		return res, err
	}
	// Count how many of the snapshot we will actually delete (sub-pages are kept).
	toDelete := 0
	for _, ch := range old {
		if ch.Type != "child_page" && ch.Type != "child_database" {
			toDelete++
		}
	}
	progress(req.Progress, "appending %d block(s)…", len(req.Blocks))
	if err := s.client.AppendBlockChildren(ctx, pageID, req.Blocks); err != nil {
		return res, err
	}
	res.BlocksWritten = len(req.Blocks)
	for _, ch := range old {
		if ch.Type == "child_page" || ch.Type == "child_database" {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("kept a %s (%s): deleting it would archive a sub-page or database", ch.Type, ch.ID))
			continue
		}
		if err := s.client.DeleteBlock(ctx, ch.ID); err != nil {
			return res, err // res.BlocksWritten is already set: the append succeeded
		}
		res.BlocksDeleted++
		progress(req.Progress, "deleted %d/%d old block(s)", res.BlocksDeleted, toDelete)
	}
	return res, nil
}

// appendBody adds req.AppendMarkdown to the end of the page, deleting nothing.
// Unlike replaceBody it makes exactly one call, so there is no partially
// applied state to converge: it either appended or it did not.
func (s *Service) appendBody(ctx context.Context, pageID string, req *BodyRequest) (BodyResult, error) {
	res := BodyResult{WasAppend: true}
	progress(req.Progress, "appending to the page body…")
	page, err := s.client.AppendPageMarkdown(ctx, pageID, req.AppendMarkdown)
	if err != nil {
		if errors.Is(err, notion.ErrAmbiguousWrite) {
			return res, &AmbiguousAppendError{err: err}
		}
		return res, err
	}
	res.Appended = true
	// The PATCH describes the page AFTER the append, so these warnings cost no
	// extra call and are never more relevant than here: appending is what
	// pushes a page past the point where Notion stops rendering it whole.
	res.Warnings = append(res.Warnings, markdownWarnings(page)...)
	return res, nil
}

// markdownWarnings renders the advisory fields of the PATCH response as facts
// about the page's state after the append.
//
// Deliberately worded differently from the CLI's bodyWarnings, which says "this
// body is truncated" — true of a body you are reading, misleading about a
// write. Here nothing was truncated: the append landed in full, and what the
// flag reports is that the page has now grown past the point where Notion will
// render it whole, which is a warning about the NEXT read.
func markdownWarnings(page notion.PageMarkdown) []string {
	var out []string
	if page.Truncated {
		out = append(out, "the append landed, but the page is now large enough that Notion "+
			"no longer renders it in full: 'get --body' will return a truncated body")
	}
	if n := len(page.UnknownBlockIDs); n > 0 {
		out = append(out, fmt.Sprintf(
			"%d block(s) on the page have no Markdown representation and read back as <unknown/>; "+
				"they are unaffected by this append", n))
	}
	return out
}

func progress(w io.Writer, format string, args ...any) {
	if w != nil {
		fmt.Fprintf(w, format+"\n", args...)
	}
}

// withBody appends the body (if any) after the properties/row already exist,
// wrapping a body failure so the caller knows properties were applied.
func (s *Service) withBody(ctx context.Context, page notion.Page, action string, body *BodyRequest) (Result, error) {
	res := Result{Action: action, Page: page}
	if body == nil {
		return res, nil
	}
	var br BodyResult
	var err error
	if body.AppendMarkdown != "" {
		br, err = s.appendBody(ctx, page.ID, body)
	} else {
		br, err = s.replaceBody(ctx, page.ID, body)
	}
	res.Body = &br
	if err != nil {
		return res, &BodyWriteError{err: err}
	}
	return res, nil
}

// findByTicket returns every row whose ticket property equals key.
func (s *Service) findByTicket(ctx context.Context, key string) ([]notion.Page, error) {
	schema, err := s.Schema(ctx)
	if err != nil {
		return nil, err
	}
	name := s.profile.Properties.Ticket
	prop, ok := schema.Properties[name]
	if !ok {
		return nil, fmt.Errorf(
			"ticket property %q does not exist in the data source; run 'notion-track doctor'", name)
	}
	filter := notion.EqualsFilter(name, prop.Type, key)
	return s.client.QueryPages(ctx, s.profile.DataSourceID, filter)
}

// findByUniqueID resolves a board id ("BDF-271", or a bare "271") to the single
// row carrying it.
//
// Most of the failures it can produce are decided before the query goes out:
// an empty input, no id column mapped, a mapped column that is missing or
// wrongly typed, an id that does not parse. Those are mistakes the user can
// fix by rewriting the command, and saying so without a round trip is both
// faster and clearer than an empty result set. Two more — no row matched, or
// more than one did — only the query itself can answer. A third category sits
// outside both: s.Schema and s.client.QueryPages can themselves fail on a
// transport error (the network down, a 401, a 429), which this function
// returns unchanged rather than folding into either list above.
func (s *Service) findByUniqueID(ctx context.Context, input string) (notion.Page, error) {
	if strings.TrimSpace(input) == "" {
		return notion.Page{}, ErrEmptyID
	}
	name := s.profile.Properties.ID
	if name == "" {
		return notion.Page{}, fmt.Errorf(
			"id addressing was requested but %w; "+
				"run 'notion-track init --id-prop <name>' to map it", ErrNoIDProperty)
	}
	schema, err := s.Schema(ctx)
	if err != nil {
		return notion.Page{}, err
	}
	prop, ok := schema.Properties[name]
	if !ok {
		return notion.Page{}, fmt.Errorf(
			"property %q is configured but does not exist in the data source; "+
				"run 'notion-track doctor' to see the current schema", name)
	}
	if prop.Type != "unique_id" {
		return notion.Page{}, fmt.Errorf(
			"id property %q has type %q, not unique_id; run 'notion-track doctor'",
			name, prop.Type)
	}
	number, err := tracker.ParseUniqueID(input, prop.Prefix)
	if err != nil {
		return notion.Page{}, err
	}
	pages, err := s.client.QueryPages(ctx, s.profile.DataSourceID,
		notion.UniqueIDEqualsFilter(name, number))
	if err != nil {
		return notion.Page{}, err
	}
	switch len(pages) {
	case 0:
		// idNotFoundError, not the %w: ErrNotFound wrapping Get uses: that
		// text reads "ticket not found", and "ticket not found: BDF-271" would
		// name a flag this command never used. idNotFoundError carries its own
		// message but still unwraps to ErrNotFound, so errors.Is and the exit
		// code are unaffected.
		return notion.Page{}, &idNotFoundError{input: input}
	case 1:
		return pages[0], nil
	default:
		// Impossible on a healthy board — the column's whole job is to be
		// unique — but "impossible" and "silently wrong" are different things,
		// and picking the first would bury the fault. Deliberately a plain
		// fmt.Errorf, not a *tracker.DuplicateError like the --ticket path's
		// equivalent case: a duplicate ticket key is an ambiguous but valid
		// state (two rows can legitimately share a name), which is what makes
		// exit 4 the right signal there. A duplicate unique_id is not an
		// ambiguous key, it is Notion's own uniqueness guarantee failing —
		// board corruption, not a naming collision — so it exits 1 like any
		// other "something is wrong with the data" failure instead.
		return notion.Page{}, fmt.Errorf(
			"id %s matches %d rows, which a unique_id column should make impossible; "+
				"run 'notion-track doctor'", input, len(pages))
	}
}

// GetByUniqueID returns the row carrying a board id, bypassing the ticket
// lookup that Get performs.
func (s *Service) GetByUniqueID(ctx context.Context, input string) (notion.Page, error) {
	return s.findByUniqueID(ctx, input)
}

// resolveOption turns what the user typed into the exact option a mapped
// column carries. Shared by the roles that live on a select, so the schema
// lookup and its two failure messages exist once rather than once per role.
//
// It runs in the service rather than inside BuildProperties because --dry-run
// builds its plan from the same Fields (see planFor): resolving here, before
// the plan is built, is what lets a dry run report the exact value the real
// write would send rather than the raw text the caller typed.
func (s *Service) resolveOption(ctx context.Context, role, query, column string) (string, error) {
	if column == "" {
		return "", fmt.Errorf(
			"%s was set to %q but no %s property is mapped; "+
				"run 'notion-track init --%s-prop <name>' to map it", role, query, role, role)
	}
	schema, err := s.Schema(ctx)
	if err != nil {
		return "", err
	}
	prop, ok := schema.Properties[column]
	if !ok {
		return "", fmt.Errorf(
			"property %q is configured but does not exist in the data source; "+
				"run 'notion-track doctor' to see the current schema", column)
	}
	return tracker.ResolveOption(role, query, prop.Options)
}

// resolveAssignee turns what the user typed into the exact option the column
// carries, and turns "me" into the configured identity.
func (s *Service) resolveAssignee(ctx context.Context, f tracker.Fields) (tracker.Fields, error) {
	if f.Assignee == "" {
		return f, nil
	}

	query := f.Assignee
	if query == "me" {
		// MeSource before Me: an unreadable credentials file leaves Me empty
		// too, and ErrNoIdentity's "configure one" is the wrong instruction
		// for a user who may already have.
		if s.profile.MeSource == config.MeSourceUnreadable {
			return f, ErrIdentityUnreadable
		}
		if s.profile.Me == "" {
			return f, ErrNoIdentity
		}
		query = s.profile.Me
	}

	resolved, err := s.resolveOption(ctx, "assignee", query, s.profile.Properties.Assignee)
	if err != nil {
		return f, err
	}
	f.Assignee = resolved
	return f, nil
}

// resolvePriority turns what the user typed into the exact option the priority
// column carries. Unlike the assignee there is no identity to translate first:
// a priority is nobody's.
func (s *Service) resolvePriority(ctx context.Context, f tracker.Fields) (tracker.Fields, error) {
	if f.Priority == "" {
		return f, nil
	}
	resolved, err := s.resolveOption(ctx, "priority", f.Priority, s.profile.Properties.Priority)
	if err != nil {
		return f, err
	}
	f.Priority = resolved
	return f, nil
}

// Upsert creates the row for a ticket or updates it if it already exists.
// body, when non-nil, replaces the page body after the properties are
// written; a nil body leaves the page body untouched.
func (s *Service) Upsert(ctx context.Context, f tracker.Fields, body *BodyRequest) (Result, error) {
	if f.Ticket == "" {
		return Result{}, ErrEmptyTicket
	}
	f, err := s.resolveAssignee(ctx, f)
	if err != nil {
		return Result{}, err
	}
	f, err = s.resolvePriority(ctx, f)
	if err != nil {
		return Result{}, err
	}
	matches, err := s.findByTicket(ctx, f.Ticket)
	if err != nil {
		return Result{}, err
	}
	decision, err := tracker.Decide(f.Ticket, matches)
	if err != nil {
		return Result{}, err
	}

	schema, err := s.Schema(ctx)
	if err != nil {
		return Result{}, err
	}
	props, err := tracker.BuildProperties(f, s.profile.Properties, schema)
	if err != nil {
		return Result{}, err
	}

	action := "updated"
	if decision.Action == tracker.ActionCreate {
		action = "created"
	}
	if s.dryRun {
		// BuildProperties above has already run, so a status the board would
		// reject fails here exactly as it would on a real run — which is most
		// of the point of asking first.
		var existing notion.Page
		if decision.Action != tracker.ActionCreate {
			existing = matches[0]
		}
		return Result{
			Action: action,
			Page:   existing,
			Plan:   planFor(action, existing.ID, existing.URL, f, s.profile.Properties, blockCount(body), appendCount(body)),
		}, nil
	}

	var page notion.Page
	if decision.Action == tracker.ActionCreate {
		page, err = s.client.CreatePage(ctx, s.profile.DataSourceID, props)
	} else {
		page, err = s.client.UpdatePage(ctx, decision.PageID, props)
	}
	if err != nil {
		return Result{Action: action}, err
	}
	return s.withBody(ctx, page, action, body)
}

// Set updates an existing row and fails if it does not exist. In CI a missing
// ticket is usually a symptom worth surfacing, not a row to conjure up. body,
// when non-nil, replaces the page body after the properties are written; a
// nil body leaves the page body untouched.
func (s *Service) Set(ctx context.Context, f tracker.Fields, body *BodyRequest) (Result, error) {
	if f.Ticket == "" {
		return Result{}, ErrEmptyTicket
	}
	f, err := s.resolveAssignee(ctx, f)
	if err != nil {
		return Result{}, err
	}
	f, err = s.resolvePriority(ctx, f)
	if err != nil {
		return Result{}, err
	}
	matches, err := s.findByTicket(ctx, f.Ticket)
	if err != nil {
		return Result{}, err
	}
	if len(matches) == 0 {
		return Result{}, fmt.Errorf("%w: %s", ErrNotFound, f.Ticket)
	}
	decision, err := tracker.Decide(f.Ticket, matches)
	if err != nil {
		return Result{}, err
	}

	schema, err := s.Schema(ctx)
	if err != nil {
		return Result{}, err
	}
	props, err := tracker.BuildProperties(f, s.profile.Properties, schema)
	if err != nil {
		return Result{}, err
	}
	if s.dryRun {
		existing := matches[0]
		return Result{
			Action: "updated",
			Page:   existing,
			Plan:   planFor("updated", existing.ID, existing.URL, f, s.profile.Properties, blockCount(body), appendCount(body)),
		}, nil
	}

	page, err := s.client.UpdatePage(ctx, decision.PageID, props)
	if err != nil {
		return Result{Action: "updated"}, err
	}
	return s.withBody(ctx, page, "updated", body)
}

// blockCount is how many blocks a body request would write, and zero when
// there is no body to write.
func blockCount(body *BodyRequest) int {
	if body == nil {
		return 0
	}
	return len(body.Blocks)
}

// appendCount is blockCount for the append path: bytes of Markdown, since
// nothing is parsed into blocks there.
func appendCount(body *BodyRequest) int {
	if body == nil {
		return 0
	}
	return len(body.AppendMarkdown)
}

// Get returns the row for a ticket.
func (s *Service) Get(ctx context.Context, ticket string) (notion.Page, error) {
	if ticket == "" {
		return notion.Page{}, ErrEmptyTicket
	}
	matches, err := s.findByTicket(ctx, ticket)
	if err != nil {
		return notion.Page{}, err
	}
	if len(matches) == 0 {
		return notion.Page{}, fmt.Errorf("%w: %s", ErrNotFound, ticket)
	}
	// Decide already makes exactly this 0/1/N choice for Upsert and produces
	// the same DuplicateError; the zero case is handled above because Get's
	// "not found" wraps ErrNotFound with the ticket key, which Decide has no
	// way to do generically.
	if _, err := tracker.Decide(ticket, matches); err != nil {
		return notion.Page{}, err
	}
	return matches[0], nil
}

// resolvePage normalizes pageID and fetches the page directly, checking that
// it belongs to this profile's data source. Both GetByID and SetByID start
// here: addressing by id skips the ticket lookup entirely, but still needs
// this one guard so a page merely shared with the integration (rather than
// a row of the configured data source) is rejected clearly instead of
// producing a confusing failure later.
//
// forWrite tells resolvePage which of the two callers it is serving, because
// they disagree on what to do when the page's parent carries no
// data_source_id at all (page.DataSourceID == ""): a read cannot do any harm
// with an unconfirmed page, so GetByID stays permissive, but a write must
// never proceed against a page whose membership it could not confirm — a
// PATCH is not reversible the way a GET is, and a page merely shared with the
// integration could coincidentally carry a property with the same name and
// type as one of the profile's, letting the write silently land on a row
// outside the configured data source. The comparison itself lives in one
// place either way, so the two callers cannot drift apart on how a confirmed
// mismatch is judged.
func (s *Service) resolvePage(ctx context.Context, pageID string, forWrite bool) (notion.Page, error) {
	if pageID == "" {
		return notion.Page{}, ErrEmptyPageID
	}
	normalized, err := notion.NormalizePageID(pageID)
	if err != nil {
		return notion.Page{}, err
	}
	page, err := s.client.GetPage(ctx, normalized)
	if err != nil {
		if errors.Is(err, notion.ErrNotFound) {
			// Notion's 404 does not distinguish "no such page" from "this
			// page exists but was never shared with the integration" — say
			// so, rather than leaving the user to guess between the two.
			return notion.Page{}, fmt.Errorf(
				"page %s not found, or not shared with this integration: %w", pageID, err)
		}
		return notion.Page{}, err
	}
	confirmedMatch := page.DataSourceID == s.profile.DataSourceID
	unconfirmedButReadOnly := page.DataSourceID == "" && !forWrite
	if !confirmedMatch && !unconfirmedButReadOnly {
		return notion.Page{}, fmt.Errorf("%w (page %s, profile data source %s)",
			ErrPageOutsideProfile, pageID, s.profile.DataSourceID)
	}
	return page, nil
}

// GetByID returns the row with the given Notion page id, bypassing the
// ticket lookup that Get performs.
func (s *Service) GetByID(ctx context.Context, pageID string) (notion.Page, error) {
	return s.resolvePage(ctx, pageID, false)
}

// GetBody returns the page body as Markdown.
//
// It takes an already-resolved page id rather than re-running the addressing
// dance: get's --ticket/--id/--page-id paths each hand back a notion.Page that
// carries the id, so the caller reuses that instead of paying for a second
// lookup.
//
// It deliberately does NOT call NormalizePageID. That function parses what a
// *user* typed -- a URL, a bare 32-hex id, a dashed UUID -- and rejects
// anything else; the id here comes from a page Notion already returned, so it
// is canonical by construction. Running it through the user-input parser would
// reject perfectly good ids that simply are not 32 hex characters.
func (s *Service) GetBody(ctx context.Context, pageID string) (notion.PageMarkdown, error) {
	if pageID == "" {
		return notion.PageMarkdown{}, ErrEmptyPageID
	}
	return s.client.GetPageMarkdown(ctx, pageID)
}

// SetByID updates the row with the given Notion page id directly.
//
// Unlike Set, it never queries by ticket key: the id alone already
// identifies exactly one row. f.Ticket is expected to be empty here — the
// caller addresses by id, not by key — and BuildProperties' "empty means
// leave alone" rule already does the right thing with that, exactly as it
// does for Set's other optional fields. body, when non-nil, replaces the
// page body after the properties are written; a nil body leaves the page
// body untouched.
func (s *Service) SetByID(ctx context.Context, pageID string, f tracker.Fields, body *BodyRequest) (Result, error) {
	f, err := s.resolveAssignee(ctx, f)
	if err != nil {
		return Result{}, err
	}
	f, err = s.resolvePriority(ctx, f)
	if err != nil {
		return Result{}, err
	}
	page, err := s.resolvePage(ctx, pageID, true)
	if err != nil {
		return Result{}, err
	}
	schema, err := s.Schema(ctx)
	if err != nil {
		return Result{}, err
	}
	props, err := tracker.BuildProperties(f, s.profile.Properties, schema)
	if err != nil {
		return Result{}, err
	}
	if s.dryRun {
		return Result{
			Action: "updated",
			Page:   page,
			Plan:   planFor("updated", page.ID, page.URL, f, s.profile.Properties, blockCount(body), appendCount(body)),
		}, nil
	}

	updated, err := s.client.UpdatePage(ctx, page.ID, props)
	if err != nil {
		return Result{Action: "updated"}, err
	}
	return s.withBody(ctx, updated, "updated", body)
}

// SetByUniqueID updates the row carrying a board id.
//
// It resolves the id to a page and hands off to SetByID: the id is a way to
// find a row, not a second way to write one, so nothing about the write itself
// is duplicated here. Resolution happens first, which is what makes a wrong id
// fail before anything is sent to the board.
//
// This ordering is the opposite of Set and SetByID, which resolve
// --assignee/--priority before locating the row. The divergence is
// deliberate, not an oversight: unlike a ticket key or a page id, a board id
// only ever names a row that already exists (Notion assigns it, nothing else
// can), so resolving it first is what makes writing to a nonexistent row
// impossible rather than merely unlikely — the same guarantee Set and
// SetByID get from checking existence before they touch the assignee or
// priority columns, just reached in the other order. One observable
// consequence: `set --ticket MISSING --assignee bogus` fails on the bad
// assignee (exit 2) before the missing ticket is ever checked, while
// `set --id BDF-999 --assignee bogus` fails on the missing row (exit 3)
// and the bad assignee is never evaluated. Reordering the write path to make
// the two consistent was considered and rejected: restructuring code this
// late in the branch is riskier than the inconsistency it would resolve.
func (s *Service) SetByUniqueID(ctx context.Context, input string, f tracker.Fields, body *BodyRequest) (Result, error) {
	page, err := s.findByUniqueID(ctx, input)
	if err != nil {
		return Result{}, err
	}
	return s.SetByID(ctx, page.ID, f, body)
}

// ListFilter is what List narrows on. Every field is optional; the zero value
// returns every row.
//
// A struct rather than a growing parameter list: the CLI, the MCP server and
// the browsing TUI all call List, and each new way to narrow a listing would
// otherwise change three signatures.
type ListFilter struct {
	Status     string
	Assignee   string
	Unassigned bool
	Priority   string
}

// ErrConflictingListFilter marks a listing narrowed both to somebody and to
// nobody.
var ErrConflictingListFilter = errors.New("cannot filter by assignee and by unassigned at the same time")

// List returns rows matching f.
func (s *Service) List(ctx context.Context, f ListFilter) ([]notion.Page, error) {
	if f.Assignee != "" && f.Unassigned {
		return nil, ErrConflictingListFilter
	}
	schema, err := s.Schema(ctx)
	if err != nil {
		return nil, err
	}

	var clauses []notion.Filter

	if f.Status != "" {
		name := s.profile.Properties.Status
		prop, ok := schema.Properties[name]
		if !ok {
			return nil, fmt.Errorf(
				"status property %q does not exist in the data source; run 'notion-track doctor'", name)
		}
		if err := tracker.ValidateStatus(f.Status, prop.Options); err != nil {
			return nil, err
		}
		clauses = append(clauses, notion.EqualsFilter(name, prop.Type, f.Status))
	}

	if f.Assignee != "" || f.Unassigned {
		name := s.profile.Properties.Assignee
		if name == "" {
			return nil, fmt.Errorf(
				"cannot filter by assignee: no assignee property is mapped; " +
					"run 'notion-track init --assignee-prop <name>' to map it")
		}
		prop, ok := schema.Properties[name]
		if !ok {
			return nil, fmt.Errorf(
				"assignee property %q does not exist in the data source; run 'notion-track doctor'", name)
		}
		if f.Unassigned {
			clauses = append(clauses, notion.IsEmptyFilter(name, prop.Type))
		} else {
			// Reuse resolveAssignee so that "me" and partial names mean exactly
			// the same thing when reading as when writing.
			resolved, err := s.resolveAssignee(ctx, tracker.Fields{Assignee: f.Assignee})
			if err != nil {
				return nil, err
			}
			clauses = append(clauses, notion.EqualsFilter(name, prop.Type, resolved.Assignee))
		}
	}

	if f.Priority != "" {
		name := s.profile.Properties.Priority
		resolved, err := s.resolveOption(ctx, "priority", f.Priority, name)
		if err != nil {
			return nil, err
		}
		// No ok check: resolveOption returned without error, so it found this
		// same column in this same memoised schema. Schema() caches under a
		// mutex, so the map read here and the one it read are the same object.
		prop := schema.Properties[name]
		clauses = append(clauses, notion.EqualsFilter(name, prop.Type, resolved))
	}

	return s.client.QueryPages(ctx, s.profile.DataSourceID, notion.AndFilter(clauses...))
}
