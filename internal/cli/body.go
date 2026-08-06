package cli

import (
	"encoding/json"
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
//
// It can exceed Notion's 500KB per-payload limit because --body-file does not
// send the file: it parses it into blocks, which splitIntoRequests then groups
// into batches each kept under maxBytesPerRequest. The file is a total, not a
// request.
const maxBodyFileBytes = 1 << 20

// maxAppendPayloadBytes is what Notion actually enforces: one payload may not
// exceed 500KB (Request limits > Size limits). This path has no splitter — the
// Markdown is sent verbatim as one insert_content PATCH — so the request is
// what has to fit, and it is the request we measure.
//
// A byte count of the FILE cannot stand in for this. JSON escaping inflates by
// a proportion, not a constant: every newline in the Markdown becomes two bytes
// inside the JSON string, so a 450KB file of short lines (a changelog, a bullet
// list) serializes to ~690KB and is refused by the API — the exact failure a
// pre-flight cap exists to prevent. appendPayloadBytes measures the real thing
// instead of guessing a margin.
const maxAppendPayloadBytes = 500 * 1000

// maxAppendFileBytes is a cheap first gate on the file, before the payload is
// built. It is NOT the real limit — maxAppendPayloadBytes is — and exists only
// so an absurdly large file is rejected without serializing it first.
//
// It sits ON the payload limit, and that is deliberately conservative rather
// than exact. Escaping only grows the content, so no file under this can be
// wrongly let through; but --expand runs AFTER this gate and can SHRINK the
// text ({{ticket}} resolves to nothing when the row was addressed by
// --page-id, the same case the re-check after expansion exists for). So a
// file of placeholders just over the limit is refused here even though it
// would have built a payload of a few dozen KB.
//
// Accepted rather than fixed by moving the gate after expansion: expanding a
// file we already know is absurdly large is the work this gate exists to
// avoid, and a Markdown file that is mostly unresolved placeholders is not
// a case worth reordering the function for.
const maxAppendFileBytes = maxAppendPayloadBytes

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
	if len(raw) > maxAppendFileBytes {
		// Its own sentence, in FILE terms. appendTooLargeMessage speaks of the
		// request it measured, and there is nothing measured here: readBodySource
		// stops one byte past the larger cap, so len(raw) is the read ceiling
		// rather than the file whenever the file is bigger still. Quoting it as
		// "builds an N-byte request" would name a number that is neither, and an
		// agent halving it would produce two more refusals.
		return nil, Errorf(ExitUsage,
			"append file %s is at least %d bytes, so the request it builds cannot fit Notion's "+
				"%d-byte limit for a single payload. An append cannot be split, so: send it in two "+
				"runs, or use --body-file, which is parsed into blocks and batched",
			path, len(raw), maxAppendPayloadBytes)
	}
	if isBlank(string(raw)) {
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
		if isBlank(string(raw)) {
			return nil, Errorf(ExitUsage,
				"append file %s expands to nothing: every placeholder in it resolved to an empty value", path)
		}
	}
	// Measured on the content that will actually be sent, and AFTER --expand:
	// expansion only ever grows the text (a 9-byte "{{date}} " becomes 11), so a
	// file legal on disk can cross the limit here. Checking only before would
	// declare it legal and let Notion refuse it.
	if n := appendPayloadBytes(string(raw)); n > maxAppendPayloadBytes {
		return nil, Errorf(ExitUsage, "%s", appendTooLargeMessage(path, n))
	}
	return &service.BodyRequest{AppendMarkdown: string(raw), Progress: progress}, nil
}

// isBlank reports whether s carries no content Notion would render.
//
// strings.TrimSpace is not enough on its own: U+FEFF (the byte-order mark) left
// White_Space in Unicode 6.3, so unicode.IsSpace excludes it and a file holding
// nothing but a BOM reads as non-empty. That is what an editor on Windows
// writes when you save an empty file as UTF-8-with-BOM — and it would reach
// Notion as content, which answers 200 and does nothing, reporting
// appended:true for a page that did not change. Exactly the silent success the
// empty guard exists to prevent, arriving through the guard.
//
// U+200B (zero-width space) has the same property and is stripped for the same
// reason. Cut everywhere rather than only at the front: a file can carry a BOM
// per concatenated part, and --expand can leave one stranded when the text
// around it resolves to nothing.
func isBlank(s string) bool {
	return strings.TrimSpace(invisibleCutter.Replace(s)) == ""
}

// Written as escapes rather than literals: Go rejects a source file containing
// a literal U+FEFF, and a literal U+200B would be invisible to anyone reading
// this line.
const (
	byteOrderMark  = "\ufeff"
	zeroWidthSpace = "\u200b"
)

var invisibleCutter = strings.NewReplacer(byteOrderMark, "", zeroWidthSpace, "")

// appendPayloadBytes returns the size of the request body AppendPageMarkdown
// will build for this content, so the pre-flight check measures what Notion
// measures rather than the file it came from.
//
// It mirrors the body in notion.AppendPageMarkdown. The duplication is
// deliberate: the alternative is exporting the payload shape from the client
// package, which would make an internal wire detail part of its API purely to
// let the CLI count bytes.
func appendPayloadBytes(content string) int {
	buf, err := json.Marshal(map[string]any{
		"type": "insert_content",
		"insert_content": map[string]any{
			"content":  content,
			"position": map[string]string{"type": "end"},
		},
	})
	if err != nil {
		// Unreachable for a map of strings, and treated as over-limit rather
		// than under: a size we could not compute must not read as "fits".
		return maxAppendPayloadBytes + 1
	}
	return len(buf)
}

// appendTooLargeMessage explains the refusal in terms of the limit that causes
// it, and says what to do instead. The size quoted is the serialized payload,
// which is what Notion weighs -- so it can legitimately exceed the file on
// disk, and the wording says so rather than looking like an arithmetic error.
func appendTooLargeMessage(path string, size int) string {
	return fmt.Sprintf(
		"append file %s builds a %d-byte request, over Notion's %d-byte limit for a single payload "+
			"(the request is larger than the file: Markdown is escaped into JSON, and --expand may "+
			"have grown it). An append cannot be split, so: send it in two runs, or use --body-file, "+
			"which is parsed into blocks and batched",
		path, size, maxAppendPayloadBytes)
}

// readBodySource reads a body/append source, one byte past the LARGER of the
// two caps so either caller's size check can still see an over-limit file. It
// deliberately does not enforce a limit itself: which cap applies depends on
// whether the content will be batched (--body-file) or sent as one payload
// (--append-file), which is the caller's knowledge, not this function's.
func readBodySource(path string, stdin io.Reader) ([]byte, error) {
	limit := int64(max(maxBodyFileBytes, maxAppendFileBytes)) + 1
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
					// appended:false alone flattens "did not happen" into the
					// same value as "outcome unknown", leaving the difference
					// only in the prose of error -- and a caller branching on
					// the boolean would re-run, which is what duplicates.
					if errors.Is(err, notion.ErrAmbiguousWrite) {
						body["ambiguous"] = true
					}
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
