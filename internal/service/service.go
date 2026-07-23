// Package service orchestrates the client, the config and the domain.
//
// It is the only layer where those three meet, which is what lets the CLI, the
// TUI and (later) the MCP adapter share one implementation of every operation.
package service

import (
	"context"
	"errors"
	"fmt"
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

// Service performs notion-track's operations against one profile.
//
// One Service may be shared by concurrent callers — the TUI runs its commands
// on separate goroutines — so the lazily fetched schema is guarded by a mutex.
type Service struct {
	client  *notion.Client
	profile config.Profile

	mu     sync.Mutex
	schema *notion.Schema // read lazily, guarded by mu
}

// New builds a Service for a profile.
func New(client *notion.Client, profile config.Profile) *Service {
	return &Service{client: client, profile: profile}
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

// Upsert creates the row for a ticket or updates it if it already exists.
func (s *Service) Upsert(ctx context.Context, f tracker.Fields) (Result, error) {
	if f.Ticket == "" {
		return Result{}, ErrEmptyTicket
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

	if decision.Action == tracker.ActionCreate {
		page, err := s.client.CreatePage(ctx, s.profile.DataSourceID, props)
		return Result{Action: "created", Page: page}, err
	}
	page, err := s.client.UpdatePage(ctx, decision.PageID, props)
	return Result{Action: "updated", Page: page}, err
}

// Set updates an existing row and fails if it does not exist. In CI a missing
// ticket is usually a symptom worth surfacing, not a row to conjure up.
func (s *Service) Set(ctx context.Context, f tracker.Fields) (Result, error) {
	if f.Ticket == "" {
		return Result{}, ErrEmptyTicket
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
	page, err := s.client.UpdatePage(ctx, decision.PageID, props)
	return Result{Action: "updated", Page: page}, err
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
func (s *Service) resolvePage(ctx context.Context, pageID string) (notion.Page, error) {
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
	// Defensive rather than fatal: a page whose parent is not a data source
	// (or whose shape omits it) leaves DataSourceID empty, and that must not
	// block addressing it by id.
	if page.DataSourceID != "" && page.DataSourceID != s.profile.DataSourceID {
		return notion.Page{}, fmt.Errorf("%w (page %s, profile data source %s)",
			ErrPageOutsideProfile, pageID, s.profile.DataSourceID)
	}
	return page, nil
}

// GetByID returns the row with the given Notion page id, bypassing the
// ticket lookup that Get performs.
func (s *Service) GetByID(ctx context.Context, pageID string) (notion.Page, error) {
	return s.resolvePage(ctx, pageID)
}

// SetByID updates the row with the given Notion page id directly.
//
// Unlike Set, it never queries by ticket key: the id alone already
// identifies exactly one row. f.Ticket is expected to be empty here — the
// caller addresses by id, not by key — and BuildProperties' "empty means
// leave alone" rule already does the right thing with that, exactly as it
// does for Set's other optional fields.
func (s *Service) SetByID(ctx context.Context, pageID string, f tracker.Fields) (Result, error) {
	page, err := s.resolvePage(ctx, pageID)
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
	updated, err := s.client.UpdatePage(ctx, page.ID, props)
	return Result{Action: "updated", Page: updated}, err
}

// List returns rows, optionally filtered by status.
func (s *Service) List(ctx context.Context, status string) ([]notion.Page, error) {
	schema, err := s.Schema(ctx)
	if err != nil {
		return nil, err
	}
	var filter notion.Filter
	if status != "" {
		name := s.profile.Properties.Status
		prop, ok := schema.Properties[name]
		if !ok {
			return nil, fmt.Errorf(
				"status property %q does not exist in the data source; run 'notion-track doctor'", name)
		}
		if err := tracker.ValidateStatus(status, prop.Options); err != nil {
			return nil, err
		}
		filter = notion.EqualsFilter(name, prop.Type, status)
	}
	return s.client.QueryPages(ctx, s.profile.DataSourceID, filter)
}
