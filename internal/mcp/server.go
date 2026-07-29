// Package mcp exposes notion-track's operations as MCP tools over stdio.
//
// It is an adapter, not a second implementation: every tool here reaches the
// same internal/service layer the CLI commands do, so a rule enforced for
// `notion-track upsert` — the duplicate check, the status validation, the
// property mapping — is enforced identically for an agent calling upsert_task.
//
// Why a local MCP server when this project exists partly because Notion's
// hosted MCP endpoint is unreachable behind a corporate firewall: that is the
// point. A server running on the user's own machine, talking to api.notion.com
// with the user's own integration token, reaches agents exactly where the
// hosted one cannot.
package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Row is one tracked row as an agent sees it. The field names match the CLI's
// --json output exactly, so an agent that has read the README's JSON contract
// finds the same keys here.
type Row struct {
	// ID is the row's board id ("BDF-271"), the identifier a person uses.
	// Empty both when the row carries no value and when the id role is not
	// mapped, so an agent never has to branch on the key's presence.
	ID             string `json:"id"`
	Ticket         string `json:"ticket"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	PageID         string `json:"page_id"`
	URL            string `json:"url"`
	LastEditedTime string `json:"last_edited_time"`
	// Assignee is empty both when nobody is assigned and when the role is not
	// mapped: the key is always present so an agent never has to branch on it.
	Assignee string `json:"assignee"`
	// Priority is empty both when the row carries no value and when the role is
	// not mapped, the same rule Assignee follows above.
	Priority string `json:"priority"`
}

// Fields are the writable values of a row. An empty field means "leave this
// alone", which is the same rule the CLI's flags follow.
//
// The field order mirrors tracker.Fields exactly: internal/cli converts one
// into the other directly, which only compiles while they stay identical.
type Fields struct {
	Ticket   string
	Title    string
	Status   string
	Due      string
	Assignee string
	Unassign bool
	Priority string
}

// ListFilter mirrors service.ListFilter field for field, so internal/cli can
// convert one into the other directly.
//
// A local type rather than an import of internal/service: this package declares
// the slice of the service layer it consumes (see Tracker) and depends on none
// of it, which is what lets a test drive the whole protocol with a fake and no
// network.
type ListFilter struct {
	Status     string
	Assignee   string
	Unassigned bool
	Priority   string
}

// Tracker is the slice of the service layer these tools need, declared here
// where it is consumed. internal/cli supplies the adapter; a test supplies a
// fake and exercises the whole protocol without a network.
type Tracker interface {
	// Upsert creates the row for a ticket or updates it, returning the row and
	// "created" or "updated".
	Upsert(ctx context.Context, f Fields) (Row, string, error)
	// Set updates an existing row and fails if it does not exist.
	Set(ctx context.Context, f Fields) (Row, string, error)
	Get(ctx context.Context, ticket string) (Row, error)
	List(ctx context.Context, f ListFilter) ([]Row, error)
}

// Tool argument types. The jsonschema tags are what the agent reads to decide
// how to call each tool, so they are documentation, not decoration.

type upsertArgs struct {
	Ticket   string `json:"ticket" jsonschema:"the ticket key identifying the row"`
	Title    string `json:"title,omitempty" jsonschema:"the row's title; omit to leave it unchanged"`
	Status   string `json:"status,omitempty" jsonschema:"the status to set; must be one the board already offers"`
	Due      string `json:"due,omitempty" jsonschema:"the due date as YYYY-MM-DD; omit to leave it unchanged"`
	Assignee string `json:"assignee,omitempty" jsonschema:"who the row belongs to; a partial name is enough when unambiguous, and 'me' means the configured identity; omit to leave it unchanged"`
	Unassign bool   `json:"unassign,omitempty" jsonschema:"clear the assignee; cannot be combined with assignee"`
	Priority string `json:"priority,omitempty" jsonschema:"how urgent the row is; must be one of the values the board offers, and a partial value is enough when unambiguous; omit to leave it unchanged"`
}

type getArgs struct {
	Ticket string `json:"ticket" jsonschema:"the ticket key to look up"`
}

type listArgs struct {
	Status     string `json:"status,omitempty" jsonschema:"only return rows with this status; omit for all rows"`
	Assignee   string `json:"assignee,omitempty" jsonschema:"only return rows assigned to this person; a partial name is enough, and 'me' means the configured identity"`
	Unassigned bool   `json:"unassigned,omitempty" jsonschema:"only return rows with no assignee; cannot be combined with assignee"`
	Priority   string `json:"priority,omitempty" jsonschema:"only return rows with this priority; a partial value is enough when unambiguous"`
}

// writeResult is what a write tool returns.
type writeResult struct {
	Action string `json:"action"` // created | updated
	Row    Row    `json:"row"`
}

type listResult struct {
	Rows []Row `json:"rows"`
}

// NewServer builds the MCP server over t. version appears in the handshake, so
// an agent can tell which build it is talking to.
func NewServer(t Tracker, version string) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{
		Name:    "notion-track",
		Version: version,
	}, nil)

	sdk.AddTool(server, &sdk.Tool{
		Name: "upsert_task",
		Description: "Create the row for a ticket key, or update it if it already exists. " +
			"Running it twice with the same ticket yields one row, not two. " +
			"Fields left empty are left unchanged.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, args upsertArgs) (*sdk.CallToolResult, writeResult, error) {
		row, action, err := t.Upsert(ctx, fieldsOf(args))
		if err != nil {
			return nil, writeResult{}, err
		}
		return textResult(fmt.Sprintf("%s %s", action, args.Ticket)),
			writeResult{Action: action, Row: row}, nil
	})

	sdk.AddTool(server, &sdk.Tool{
		Name: "set_task",
		Description: "Update the row for a ticket key that already exists. " +
			"Fails if there is no such row, rather than creating one — use upsert_task " +
			"when the row may not exist yet. Fields left empty are left unchanged.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, args upsertArgs) (*sdk.CallToolResult, writeResult, error) {
		row, action, err := t.Set(ctx, fieldsOf(args))
		if err != nil {
			return nil, writeResult{}, err
		}
		return textResult(fmt.Sprintf("%s %s", action, args.Ticket)),
			writeResult{Action: action, Row: row}, nil
	})

	sdk.AddTool(server, &sdk.Tool{
		Name:        "get_task",
		Description: "Read the row for one ticket key.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, args getArgs) (*sdk.CallToolResult, Row, error) {
		row, err := t.Get(ctx, args.Ticket)
		if err != nil {
			return nil, Row{}, err
		}
		return textResult(fmt.Sprintf("%s — %s [%s]", row.Ticket, row.Title, row.Status)), row, nil
	})

	sdk.AddTool(server, &sdk.Tool{
		Name: "list_tasks",
		Description: "List the tracked rows, optionally narrowed by status, by assignee, " +
			"or to only those with no assignee.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, args listArgs) (*sdk.CallToolResult, listResult, error) {
		rows, err := t.List(ctx, ListFilter(args))
		if err != nil {
			return nil, listResult{}, err
		}
		return textResult(fmt.Sprintf("%d rows", len(rows))), listResult{Rows: rows}, nil
	})

	return server
}

// Run serves the protocol on stdio until the peer disconnects.
//
// stdout carries JSON-RPC and nothing else: anything printed there by mistake
// is a protocol error, which is why nothing in this package writes to it.
func Run(ctx context.Context, t Tracker, version string) error {
	return NewServer(t, version).Run(ctx, &sdk.StdioTransport{})
}

// fieldsOf converts the tool's arguments into the service's fields. The two
// structs are deliberately identical, so this is a conversion rather than a
// copy — and stops compiling the moment they diverge.
func fieldsOf(a upsertArgs) Fields { return Fields(a) }

// textResult is the human-readable half of a tool response. The structured
// half is what an agent should act on; this is the one-line summary a
// transcript shows.
func textResult(summary string) *sdk.CallToolResult {
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: summary}},
	}
}
