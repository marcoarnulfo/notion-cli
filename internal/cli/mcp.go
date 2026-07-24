package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/marcoarnulfo/notion-cli/internal/mcp"
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

// mcpVersion is what the MCP handshake reports. The binary has no build-stamped
// version yet — that arrives with the GoReleaser pipeline (issue #1), which
// will wire the release tag through here.
const mcpVersion = "dev"

// runMCPServer is the seam that keeps the blocking stdio server out of the
// tests: it reads from stdin until the peer disconnects, which a test has no
// peer to do.
var runMCPServer = func(ctx context.Context, t mcp.Tracker) error {
	return mcp.Run(ctx, t, mcpVersion)
}

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve notion-track's operations as MCP tools over stdio",
		Long: "Serve notion-track's operations as MCP tools over stdio.\n\n" +
			"An adapter, not a second implementation: every tool reaches the same\n" +
			"code the CLI commands do, so the duplicate check, the status validation\n" +
			"and the property mapping behave identically for an agent.\n\n" +
			"stdout carries the JSON-RPC protocol and nothing else. Point an MCP\n" +
			"client at 'notion-track mcp' as its command.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			return runMCPServer(cmd.Context(), trackerAdapter{svc: svc})
		},
	}
}

// trackerAdapter turns the service into what the MCP tools consume, mirroring
// what boardAdapter does for the browsing TUI.
//
// The rows it returns are converted straight from pageJSON, the CLI's --json
// shape. That conversion is deliberate and load-bearing: it only compiles
// while the two structs stay identical, so the documented JSON contract and
// what an agent sees cannot drift apart unnoticed.
type trackerAdapter struct{ svc *service.Service }

func (a trackerAdapter) Upsert(ctx context.Context, f mcp.Fields) (mcp.Row, string, error) {
	res, err := a.svc.Upsert(ctx, fieldsFromMCP(f), nil)
	if err != nil {
		return mcp.Row{}, "", err
	}
	return mcp.Row(toPageJSON(res.Page, a.svc.Profile().Properties)), res.Action, nil
}

func (a trackerAdapter) Set(ctx context.Context, f mcp.Fields) (mcp.Row, string, error) {
	res, err := a.svc.Set(ctx, fieldsFromMCP(f), nil)
	if err != nil {
		return mcp.Row{}, "", err
	}
	return mcp.Row(toPageJSON(res.Page, a.svc.Profile().Properties)), res.Action, nil
}

func (a trackerAdapter) Get(ctx context.Context, ticket string) (mcp.Row, error) {
	page, err := a.svc.Get(ctx, ticket)
	if err != nil {
		return mcp.Row{}, err
	}
	return mcp.Row(toPageJSON(page, a.svc.Profile().Properties)), nil
}

func (a trackerAdapter) List(ctx context.Context, status string) ([]mcp.Row, error) {
	pages, err := a.svc.List(ctx, status)
	if err != nil {
		return nil, err
	}
	props := a.svc.Profile().Properties
	rows := make([]mcp.Row, 0, len(pages))
	for _, p := range pages {
		rows = append(rows, mcp.Row(toPageJSON(p, props)))
	}
	return rows, nil
}

func fieldsFromMCP(f mcp.Fields) tracker.Fields {
	return tracker.Fields{Ticket: f.Ticket, Title: f.Title, Status: f.Status, Due: f.Due}
}
