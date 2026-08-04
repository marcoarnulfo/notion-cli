package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/markdown"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/marcoarnulfo/notion-cli/internal/template"
	"github.com/spf13/cobra"
)

// maxBodyFileBytes is the pre-flight cap on a --body-file (spec §9): a task
// body over 1 MiB of Markdown is out of scope, and rejecting it up front beats
// dying mid-replace.
const maxBodyFileBytes = 1 << 20

// now is the clock, as a seam: --expand's {{date}} has to be assertable in a
// test without the test knowing what day it is run on.
var now = time.Now

// loadBody reads and parses a --body-file into a validated BodyRequest, all
// before any network call. path "-" reads stdin. Every input problem is a
// usage error (exit 2). progress is where the service later writes ephemeral
// progress lines (stderr). vars, when non-nil, expands placeholders before
// parsing.
func loadBody(path string, stdin io.Reader, progress io.Writer, vars map[string]string) (*service.BodyRequest, []string, error) {
	raw, err := readBodySource(path, stdin)
	if err != nil {
		return nil, nil, Errorf(ExitUsage, "reading body file %s: %v", path, err)
	}
	if len(raw) > maxBodyFileBytes {
		return nil, nil, Errorf(ExitUsage, "body file %s is over the %d-byte limit", path, maxBodyFileBytes)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil, nil, Errorf(ExitUsage, "body file %s is empty", path)
	}
	// Before parsing, not after: the expanded text is what the author meant to
	// write, so Markdown structure a value introduces (a ticket key inside a
	// heading, say) is honoured, and the parser never sees braces it would
	// have to carry through untouched.
	if vars != nil {
		expanded, err := template.Expand(string(raw), vars)
		if err != nil {
			return nil, nil, Errorf(ExitUsage, "%s: %v", path, err)
		}
		raw = []byte(expanded)
	}
	blocks, warnings, err := markdown.ToBlocks(raw)
	if err != nil {
		return nil, nil, Errorf(ExitUsage, "parsing %s: %v", path, err)
	}
	if err := notion.ValidateAppendable(blocks); err != nil {
		return nil, nil, Errorf(ExitUsage, "%v", err)
	}
	return &service.BodyRequest{Blocks: blocks, Progress: progress}, warnings, nil
}

// loadAppendBody reads a --append-file into a BodyRequest that appends rather
// than replaces. Unlike loadBody it does NOT parse Markdown into blocks: the
// append endpoint takes Markdown directly and parses it server-side, so
// goldmark and ValidateAppendable are not in this path at all.
//
// The empty check is load-bearing rather than defensive: Notion answers 200
// and does nothing for empty content, so without it an empty file would
// report success while changing nothing.
func loadAppendBody(path string, stdin io.Reader, progress io.Writer, vars map[string]string) (*service.BodyRequest, error) {
	raw, err := readBodySource(path, stdin)
	if err != nil {
		return nil, Errorf(ExitUsage, "reading append file %s: %v", path, err)
	}
	if len(raw) > maxBodyFileBytes {
		return nil, Errorf(ExitUsage, "append file %s is over the %d-byte limit", path, maxBodyFileBytes)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil, Errorf(ExitUsage, "append file %s is empty", path)
	}
	if vars != nil {
		expanded, err := template.Expand(string(raw), vars)
		if err != nil {
			return nil, Errorf(ExitUsage, "%s: %v", path, err)
		}
		raw = []byte(expanded)
		// Checked again AFTER expanding, not only before: a file holding just
		// "{{ticket}}" is non-empty on disk but expands to nothing when the row
		// was addressed by --page-id or --id, and would reach Notion as the
		// silent no-op the first check exists to prevent.
		if strings.TrimSpace(string(raw)) == "" {
			return nil, Errorf(ExitUsage,
				"append file %s expands to nothing: every placeholder in it resolved to an empty value", path)
		}
	}
	return &service.BodyRequest{AppendMarkdown: string(raw), Progress: progress}, nil
}

func readBodySource(path string, stdin io.Reader) ([]byte, error) {
	// Read one byte past the cap so the size check can detect an over-limit file.
	limit := int64(maxBodyFileBytes) + 1
	if path == "-" {
		if stdin == nil {
			return nil, fmt.Errorf("no stdin")
		}
		return io.ReadAll(io.LimitReader(stdin, limit))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// emitPlan renders what --dry-run would have done.
//
// The human form goes to stdout, like every other result: it is the answer to
// the question that was asked, not a diagnostic about it.
func emitPlan(cmd *cobra.Command, plan *service.Plan, asJSON bool) error {
	if asJSON {
		return printJSON(cmd.OutOrStdout(), map[string]any{"dry_run": true, "plan": plan})
	}

	verb := "would update"
	if plan.Action == "created" {
		verb = "would create"
	}
	if plan.PageID != "" {
		cmd.Printf("%s %s\n", verb, plan.PageID)
	} else {
		cmd.Printf("%s a new row\n", verb)
	}
	for _, p := range plan.Properties {
		cmd.Printf("  %-20s %s\n", p.Column, p.Value)
	}
	for _, column := range plan.Cleared {
		cmd.Printf("  %-20s %s\n", "clear", column)
	}
	if plan.BodyBlocks > 0 {
		cmd.Printf("  %-20s %d blocks (replacing the current body)\n", "page body", plan.BodyBlocks)
	}
	if plan.AppendBytes > 0 {
		cmd.Printf("  %-20s %d bytes (appending to the current body)\n", "page body", plan.AppendBytes)
	}
	if plan.URL != "" {
		cmd.Printf("  %s\n", plan.URL)
	}
	return nil
}

// printWarnings writes each warning to w (stderr) with a "warning: " prefix.
func printWarnings(w io.Writer, warnings []string) {
	for _, msg := range warnings {
		fmt.Fprintln(w, "warning: "+msg)
	}
}

// emitWrite is the shared output path for upsert/set: it prints warnings to
// stderr, then either the success --json (with the additive body object) or,
// on a body failure after properties were written, a parsable partial-failure
// --json, and finally returns err so the process exits with the right code.
func emitWrite(cmd *cobra.Command, props config.Properties, res service.Result, warnings []string, asJSON bool, err error) error {
	printWarnings(cmd.ErrOrStderr(), warnings)
	// A dry run reports what it would have done and stops. It cannot reach the
	// partial-failure path below, because nothing was written for a body write
	// to fail after.
	if err == nil && res.Plan != nil {
		return emitPlan(cmd, res.Plan, asJSON)
	}
	if res.Body != nil {
		printWarnings(cmd.ErrOrStderr(), res.Body.Warnings)
	}
	if err != nil {
		var bwe *service.BodyWriteError
		if errors.As(err, &bwe) && asJSON {
			body := map[string]any{"written": false, "error": bwe.Error()}
			if res.Body != nil {
				if res.Body.WasAppend {
					// An append either landed or it did not; there are no
					// partial counts, and borrowing the replace path's would
					// claim "0 blocks written" about an operation that never
					// counted blocks.
					body["appended"] = res.Body.Appended
				} else {
					// Real counts of what happened before the failure: crucial in the
					// dual case (append ok, a DELETE failed) where the body WAS written
					// (spec §8).
					body["blocks_written"] = res.Body.BlocksWritten
					body["blocks_deleted"] = res.Body.BlocksDeleted
				}
			}
			_ = printJSON(cmd.OutOrStdout(), map[string]any{
				"action": res.Action,
				"page":   toPageJSON(res.Page, props),
				"body":   body,
			})
		}
		return err
	}
	if asJSON {
		out := map[string]any{"action": res.Action, "page": toPageJSON(res.Page, props)}
		if res.Body != nil {
			// An append and a replace are different operations with different
			// consequences: report the counters that actually apply, so a
			// script never has to infer which one ran. Branch on WasAppend
			// (which mode ran), not on Appended (whether it landed).
			if res.Body.WasAppend {
				out["body"] = map[string]any{"appended": res.Body.Appended}
			} else {
				out["body"] = map[string]any{
					"blocks_written": res.Body.BlocksWritten,
					"blocks_deleted": res.Body.BlocksDeleted,
				}
			}
		}
		return printJSON(cmd.OutOrStdout(), out)
	}
	return nil
}
