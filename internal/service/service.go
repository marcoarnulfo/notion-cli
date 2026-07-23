// Package service orchestrates the client, the config and the domain.
//
// It is the only layer where those three meet, which is what lets the CLI, the
// TUI and (later) the MCP adapter share one implementation of every operation.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

// ErrNotFound means no row carries the requested ticket key.
var ErrNotFound = errors.New("ticket not found")

// Service performs notion-track's operations against one profile.
type Service struct {
	client  *notion.Client
	profile config.Profile
	schema  *notion.Schema // read lazily, once
}

// New builds a Service for a profile.
func New(client *notion.Client, profile config.Profile) *Service {
	return &Service{client: client, profile: profile}
}

// Schema returns the data source schema, fetching it at most once.
func (s *Service) Schema(ctx context.Context) (*notion.Schema, error) {
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
	matches, err := s.findByTicket(ctx, ticket)
	if err != nil {
		return notion.Page{}, err
	}
	if len(matches) == 0 {
		return notion.Page{}, fmt.Errorf("%w: %s", ErrNotFound, ticket)
	}
	if len(matches) > 1 {
		return notion.Page{}, &tracker.DuplicateError{Ticket: ticket, Pages: matches}
	}
	return matches[0], nil
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
