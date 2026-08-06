package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/spf13/cobra"
)

// cliProps returns the config.Properties the stub config in withStubbedAPI
// maps: ticket=Ticket, status=Stato, title=Name.
func cliProps() config.Properties {
	return config.Properties{Ticket: "Ticket", Status: "Stato", Title: "Name"}
}

func TestLoadBodyRejectsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.md")
	os.WriteFile(p, []byte("   \n"), 0o600)
	_, _, err := loadBody(p, nil, nil, nil)
	if err == nil || exitCodeFor(err) != ExitUsage {
		t.Fatalf("empty file must be a usage error, got %v (code %d)", err, exitCodeFor(err))
	}
}

// TestLoadBodyRejectsFileOverOneMiB pins the pre-flight size cap (spec §9): a
// body file just over 1MiB must be rejected as a usage error (exit 2) before
// any parsing or network call.
func TestLoadBodyRejectsFileOverOneMiB(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "huge.md")
	data := bytes.Repeat([]byte("a"), (1<<20)+1)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write huge fixture: %v", err)
	}
	_, _, err := loadBody(p, nil, nil, nil)
	if err == nil {
		t.Fatal("want error for a body file over 1MiB, got nil")
	}
	if exitCodeFor(err) != ExitUsage {
		t.Fatalf("over-limit body file must be a usage error, got %v (code %d)", err, exitCodeFor(err))
	}
}

func TestLoadBodyRejectsMissingFile(t *testing.T) {
	_, _, err := loadBody(filepath.Join(t.TempDir(), "nope.md"), nil, nil, nil)
	if err == nil || exitCodeFor(err) != ExitUsage {
		t.Fatalf("missing file must be a usage error, got %v", err)
	}
}

func TestLoadBodyReadsStdin(t *testing.T) {
	req, _, err := loadBody("-", strings.NewReader("# Title\n\nbody\n"), nil, nil)
	if err != nil {
		t.Fatalf("stdin body: %v", err)
	}
	if len(req.Blocks) == 0 {
		t.Fatal("stdin produced no blocks")
	}
}

// Partial failure e2e: properties are written, then the body append 400s. The
// command must exit 1 (NOT 2, despite the underlying 400) and, with --json,
// still print a parsable object marking the body as unwritten. This is the
// most delicate public contract of the feature (spec §8).
func TestUpsertBodyAppendFailureExitsOneWithParsableJSON(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == http.MethodGet:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"code":"validation_error","message":"bad block"}`))
		default: // PATCH /v1/pages/{id}
			w.Write([]byte(cliRowJSON))
		}
	})
	md := filepath.Join(t.TempDir(), "body.md")
	os.WriteFile(md, []byte("# Title\n\nbody\n"), 0o600)

	var code int
	out := captureStdout(t, func() {
		code = executeArgs([]string{"upsert", "--ticket", "BDF-231", "--body-file", md, "--json", "--config", cfg})
	})
	if code != ExitError {
		t.Fatalf("partial failure must exit %d, got %d", ExitError, code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout must stay parsable JSON on partial failure: %v\n%s", err, out)
	}
	body, ok := got["body"].(map[string]any)
	if !ok || body["written"] != false || body["error"] == nil {
		t.Fatalf("body must mark written:false with an error: %v", got)
	}
	if got["page"] == nil {
		t.Fatal("page (applied properties) must still be present")
	}
}

// emitWrite must route warnings to stderr, never stdout (which carries --json).
func TestEmitWriteSendsWarningsToStderrNotStdout(t *testing.T) {
	cmd := &cobra.Command{}
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)

	res := service.Result{Action: "updated", Body: &service.BodyResult{Warnings: []string{"kept a child_page"}}}
	_ = emitWrite(cmd, cliProps(), res, []string{"table degraded"}, false, nil)

	if !strings.Contains(errBuf.String(), "table degraded") || !strings.Contains(errBuf.String(), "child_page") {
		t.Fatalf("warnings must be on stderr: %q", errBuf.String())
	}
	if strings.Contains(out.String(), "warning") {
		t.Fatalf("warnings must NOT be on stdout: %q", out.String())
	}
}

// withFixedClock pins --expand's {{date}} so a test can assert on the value
// rather than recompute it the same way the code does, which would pass even
// if both were wrong.
func withFixedClock(t *testing.T, day string) {
	t.Helper()
	when, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	old := now
	now = func() time.Time { return when }
	t.Cleanup(func() { now = old })
}

func TestLoadBodyExpandsPlaceholdersWhenAskedTo(t *testing.T) {
	p := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(p, []byte("Closed {{ticket}} on {{date}}.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	req, _, err := loadBody(p, nil, nil, map[string]string{"ticket": "BDF-231", "date": "2026-07-24"})
	if err != nil {
		t.Fatal(err)
	}

	// Asserted fragment by fragment: the Markdown parser is free to split one
	// paragraph across several rich-text runs, which changes nothing about
	// what Notion renders.
	got := blockText(t, req.Blocks)
	for _, want := range []string{"BDF-231", "2026-07-24"} {
		if !strings.Contains(got, want) {
			t.Errorf("body = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "{{") {
		t.Errorf("body = %q, still carries a placeholder", got)
	}
}

// The flag governs the whole feature: off, there are no variables at all, so
// loadBody skips expansion entirely.
func TestBodyVarsOnlyExistWhenExpandIsAsked(t *testing.T) {
	withFixedClock(t, "2026-07-24")
	wf := writeFlags{ticket: "BDF-231"}

	if got := wf.bodyVars(); got != nil {
		t.Fatalf("bodyVars = %v without --expand, want none", got)
	}

	wf.expand = true
	vars := wf.bodyVars()
	if vars["ticket"] != "BDF-231" {
		t.Errorf("ticket = %q", vars["ticket"])
	}
	if vars["date"] != "2026-07-24" {
		t.Errorf("date = %q, want today in ISO form", vars["date"])
	}
}

// The default must stay exactly what it was before --expand existed: a body
// that legitimately contains braces has to keep working.
func TestLoadBodyLeavesPlaceholdersAloneByDefault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(p, []byte("Use {{ticket}} in your template.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	req, _, err := loadBody(p, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := blockText(t, req.Blocks); !strings.Contains(got, "{{ticket}}") {
		t.Errorf("body = %q, want the braces untouched", got)
	}
}

func TestLoadBodyRejectsAnUnknownPlaceholderAsAUsageError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(p, []byte("see {{tikcet}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := loadBody(p, nil, nil, map[string]string{"ticket": "BDF-231"})

	if err == nil {
		t.Fatal("a typo'd placeholder was accepted")
	}
	if code := exitCodeFor(err); code != ExitUsage {
		t.Errorf("exit code = %d, want %d (usage)", code, ExitUsage)
	}
	if !strings.Contains(err.Error(), "tikcet") {
		t.Errorf("error = %q, want it to name the placeholder", err)
	}
}

// blockText flattens the rich text of every block into one string, which is
// all these tests need to see.
func blockText(t *testing.T, blocks []notion.Block) string {
	t.Helper()
	raw, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// dryRunAPI answers the reads a dry run makes and fails loudly on any write:
// the guarantee under test is that none happen.
func dryRunAPI(t *testing.T, queryResults string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + queryResults + `],"has_more":false}`))
		default:
			t.Errorf("a dry run reached %s %s", r.Method, r.URL.Path)
			w.Write([]byte(cliRowJSON))
		}
	}
}

func TestUpsertDryRunReportsWithoutWriting(t *testing.T) {
	cfg := withStubbedAPI(t, dryRunAPI(t, cliRowJSON))

	out := captureStdout(t, func() {
		code := executeArgs([]string{
			"upsert", "--ticket", "BDF-231", "--status", "Fatto",
			"--dry-run", "--config", cfg,
		})
		if code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	if !strings.Contains(out, "would update") {
		t.Errorf("output does not say what would happen:\n%s", out)
	}
	for _, want := range []string{"Ticket", "BDF-231", "Stato", "Fatto"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not name %q:\n%s", want, out)
		}
	}
}

func TestUpsertDryRunOnANewTicketSaysItWouldCreate(t *testing.T) {
	cfg := withStubbedAPI(t, dryRunAPI(t, ""))

	out := captureStdout(t, func() {
		if code := executeArgs([]string{
			"upsert", "--ticket", "BDF-999", "--title", "New", "--dry-run", "--config", cfg,
		}); code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	if !strings.Contains(out, "would create") {
		t.Errorf("output = %q, want it to say it would create", out)
	}
}

// --json stays the scripting contract: a dry run has to be recognisable as one
// rather than passing for a write that happened.
func TestDryRunJSONIsMarkedAsSuch(t *testing.T) {
	cfg := withStubbedAPI(t, dryRunAPI(t, cliRowJSON))

	out := captureStdout(t, func() {
		if code := executeArgs([]string{
			"upsert", "--ticket", "BDF-231", "--status", "Fatto",
			"--dry-run", "--json", "--config", cfg,
		}); code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	if got["dry_run"] != true {
		t.Errorf("dry_run = %v, want true: %v", got["dry_run"], got)
	}
	plan, ok := got["plan"].(map[string]any)
	if !ok || plan["action"] != "updated" {
		t.Fatalf("plan = %v", got["plan"])
	}
}

func TestSetDryRunReportsWithoutWriting(t *testing.T) {
	cfg := withStubbedAPI(t, dryRunAPI(t, cliRowJSON))

	out := captureStdout(t, func() {
		if code := executeArgs([]string{
			"set", "--ticket", "BDF-231", "--status", "Fatto", "--dry-run", "--config", cfg,
		}); code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	if !strings.Contains(out, "would update") {
		t.Errorf("output = %q", out)
	}
}

// The body is parsed and counted, but never sent.
func TestDryRunCountsTheBodyWithoutWritingIt(t *testing.T) {
	cfg := withStubbedAPI(t, dryRunAPI(t, cliRowJSON))
	md := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(md, []byte("# One\n\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if code := executeArgs([]string{
			"upsert", "--ticket", "BDF-231", "--body-file", md, "--dry-run", "--config", cfg,
		}); code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	if !strings.Contains(out, "page body") || !strings.Contains(out, "blocks") {
		t.Errorf("output does not mention the body:\n%s", out)
	}
}

// A dry run with --append-file must say so: today's blockCount only counts
// Blocks, so without AppendBytes an append dry run would report the
// properties and stay completely silent about the one thing that command was
// asked about.
func TestDryRunReportsAppendWithoutWritingIt(t *testing.T) {
	cfg := withStubbedAPI(t, dryRunAPI(t, cliRowJSON))
	md := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(md, []byte("Ticket closed."), 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if code := executeArgs([]string{
			"set", "--ticket", "BDF-231", "--append-file", md, "--dry-run", "--config", cfg,
		}); code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	if !strings.Contains(out, "page body") || !strings.Contains(out, "bytes") || !strings.Contains(out, "appending") {
		t.Errorf("output does not mention the append:\n%s", out)
	}
}

// A dry run that reported a happy plan for a write the real run would reject
// is worse than useless: it would send the user off to run the real thing.
func TestDryRunStillFailsOnAnInvalidStatus(t *testing.T) {
	cfg := withStubbedAPI(t, dryRunAPI(t, cliRowJSON))

	code := executeArgs([]string{
		"upsert", "--ticket", "BDF-231", "--status", "Nonexistent", "--dry-run", "--config", cfg,
	})

	if code == ExitOK {
		t.Fatal("a dry run accepted a status the data source rejects")
	}
}

func TestLoadAppendBodyKeepsRawMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte("## Update\nDone."), 0o600); err != nil {
		t.Fatal(err)
	}
	req, err := loadAppendBody(path, nil, io.Discard, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Raw Markdown, not blocks: the append endpoint parses it server-side.
	if req.AppendMarkdown != "## Update\nDone." {
		t.Errorf("AppendMarkdown = %q", req.AppendMarkdown)
	}
	if len(req.Blocks) != 0 {
		t.Errorf("the append path must not build blocks, got %d", len(req.Blocks))
	}
}

// Notion answers 200 and does nothing for empty content (spec §10.e), so this
// check is the only thing between a user and a successful-looking no-op.
func TestLoadAppendBodyRejectsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(path, []byte("   \n\t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAppendBody(path, nil, io.Discard, nil); err == nil {
		t.Fatal("an empty append file must be a usage error, not a silent no-op")
	}
}

func TestLoadAppendBodyExpandsPlaceholders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte("Ticket {{ticket}} done."), 0o600); err != nil {
		t.Fatal(err)
	}
	req, err := loadAppendBody(path, nil, io.Discard, map[string]string{"ticket": "T-9", "date": "2026-08-04"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.AppendMarkdown != "Ticket T-9 done." {
		t.Errorf("AppendMarkdown = %q", req.AppendMarkdown)
	}
}

func TestLoadAppendBodyReadsStdin(t *testing.T) {
	req, err := loadAppendBody("-", strings.NewReader("from stdin"), io.Discard, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.AppendMarkdown != "from stdin" {
		t.Errorf("AppendMarkdown = %q", req.AppendMarkdown)
	}
}

// A file that is non-empty on disk but expands to nothing would slip past the
// pre-expansion check and reach Notion as a silent no-op.
func TestLoadAppendBodyRejectsFileThatExpandsToNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte("{{ticket}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Addressing by --page-id leaves ticket empty, which is what makes this
	// reachable in practice.
	_, err := loadAppendBody(path, nil, io.Discard, map[string]string{"ticket": "", "date": "2026-08-04"})
	if err == nil {
		t.Fatal("a file that expands to nothing must be rejected, not sent as an empty append")
	}
}

// The empty-file check must stop the run BEFORE any network call: Notion
// answers 200 for an empty append, so reaching it at all defeats the guard.
func TestSetAppendEmptyFileMakesNoHTTPCall(t *testing.T) {
	var calls int32
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := executeArgs([]string{"set", "--ticket", "BDF-231", "--append-file", empty, "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("an empty append must be rejected before any request, got %d calls", n)
	}
}

func TestSetRejectsBodyFileWithAppendFile(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code := executeArgs([]string{"set", "--ticket", "BDF-231",
		"--body-file", a, "--append-file", b, "--config", cfg})
	if code != ExitUsage {
		t.Fatalf("--body-file with --append-file must exit %d (replace and append are different intents), got %d",
			ExitUsage, code)
	}
}

// appended and blocks_written are different facts about different operations:
// a script must not have to infer which one ran.
func TestEmitWriteJSONReportsAppendDistinctly(t *testing.T) {
	cmd := &cobra.Command{}
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)

	res := service.Result{
		Action: "updated",
		Page:   notion.Page{ID: "p1", URL: "https://notion.so/p1"},
		Body:   &service.BodyResult{WasAppend: true, Appended: true},
	}
	if err := emitWrite(cmd, cliProps(), res, nil, true, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		Body map[string]any `json:"body"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if got.Body["appended"] != true {
		t.Errorf(`body.appended = %v, want true`, got.Body["appended"])
	}
	if _, present := got.Body["blocks_written"]; present {
		t.Error("an append must not report blocks_written: that is the replace path's counter")
	}
}

// The replace path must keep reporting exactly what it always did.
func TestEmitWriteJSONKeepsReplaceCounters(t *testing.T) {
	cmd := &cobra.Command{}
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)

	res := service.Result{
		Action: "updated",
		Page:   notion.Page{ID: "p1"},
		Body:   &service.BodyResult{BlocksWritten: 3, BlocksDeleted: 2},
	}
	if err := emitWrite(cmd, cliProps(), res, nil, true, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		Body map[string]any `json:"body"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if got.Body["blocks_written"] != float64(3) || got.Body["blocks_deleted"] != float64(2) {
		t.Errorf("replace counters changed: %v", got.Body)
	}
	if _, present := got.Body["appended"]; present {
		t.Error("a replace must not report appended")
	}
}

// A FAILED append must say appended:false -- not borrow the replace path's
// counters and claim "0 blocks written" about an operation that never counted
// blocks.
//
// Driven end to end rather than by handing emitWrite a *BodyWriteError:
// its field is unexported (service.go:181), so package cli cannot build one.
// This mirrors the existing partial-failure test in this file, which makes the
// properties write succeed and the body write fail.
func TestSetAppendPartialFailureReportsAppendedFalse(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/markdown"):
			// The append fails AFTER the properties were written.
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"object":"error","status":500,"code":"internal_server_error","message":"boom"}`))
		case r.Method == http.MethodPatch:
			w.Write([]byte(cliRowJSON)) // the properties write succeeds
		default:
			w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
		}
	})
	note := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(note, []byte("## Progress\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureStdout(t, func() {
		code = executeArgs([]string{"set", "--ticket", "BDF-231",
			"--append-file", note, "--json", "--config", cfg})
	})
	if code != ExitError {
		t.Fatalf("a partial failure must exit %d, got %d", ExitError, code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout must stay parsable JSON on partial failure: %v\n%s", err, out)
	}
	body, ok := got["body"].(map[string]any)
	if !ok {
		t.Fatalf("body missing: %v", got)
	}
	if body["appended"] != false {
		t.Errorf(`body.appended = %v, want false`, body["appended"])
	}
	if _, present := body["blocks_written"]; present {
		t.Error("a failed append must not report blocks_written")
	}
	// A 500 on a non-idempotent write is ambiguous, not a clean failure: the
	// append may have landed. appended:false alone cannot say that, and a
	// caller that reads it as "nothing happened" re-runs and duplicates.
	if body["ambiguous"] != true {
		t.Errorf(`body.ambiguous = %v, want true: appended:false must not read as "nothing happened"`,
			body["ambiguous"])
	}
	// The prose must not carry the sentinel's own contradicting advice.
	if msg, _ := body["error"].(string); strings.Contains(msg, "re-run to converge") {
		t.Errorf("the error must not tell an agent to re-run an append: %q", msg)
	}
	if got["page"] == nil {
		t.Error("page (applied properties) must still be present")
	}
}

// The counterpart: a 400 is a clean refusal, so there is no ambiguity to
// report and the flag must stay absent rather than always being emitted.
func TestSetAppendCleanRejectionIsNotAmbiguous(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/markdown"):
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"object":"error","status":400,"code":"validation_error","message":"nope"}`))
		case r.Method == http.MethodPatch:
			w.Write([]byte(cliRowJSON))
		default:
			w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
		}
	})
	note := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(note, []byte("## Progress\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		executeArgs([]string{"set", "--ticket", "BDF-231",
			"--append-file", note, "--json", "--config", cfg})
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout must stay parsable JSON: %v\n%s", err, out)
	}
	body, ok := got["body"].(map[string]any)
	if !ok {
		t.Fatalf("body missing: %v", got)
	}
	if _, present := body["ambiguous"]; present {
		t.Errorf("a 400 is a clean refusal: ambiguous must be absent, got %v", body)
	}
}

// The whole path, flag to HTTP: --append-file must reach Notion as one
// insert_content PATCH and delete nothing.
func TestSetAppendFileEndToEnd(t *testing.T) {
	var patched map[string]any
	var deletes int32
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case r.Method == http.MethodDelete:
			atomic.AddInt32(&deletes, 1)
			fmt.Fprint(w, `{}`)
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/markdown"):
			_ = json.NewDecoder(r.Body).Decode(&patched)
			fmt.Fprint(w, `{"object":"page_markdown","id":"page1","markdown":"old\nnew",
				"truncated":false,"unknown_block_ids":[]}`)
		case r.Method == http.MethodPatch:
			// the properties write
			fmt.Fprint(w, cliRowJSON)
		default:
			fmt.Fprint(w, `{"results":[`+cliRowJSON+`],"has_more":false}`)
		}
	})

	dir := t.TempDir()
	note := filepath.Join(dir, "note.md")
	if err := os.WriteFile(note, []byte("## Progress\nShipped."), 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"set", "--ticket", "BDF-231",
			"--append-file", note, "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	if patched == nil {
		t.Fatal("no append reached Notion")
	}
	if patched["type"] != "insert_content" {
		t.Errorf(`type = %v, want "insert_content"`, patched["type"])
	}
	ic, ok := patched["insert_content"].(map[string]any)
	if !ok {
		t.Fatalf("insert_content missing: %v", patched)
	}
	if ic["content"] != "## Progress\nShipped." {
		t.Errorf("content = %v", ic["content"])
	}
	if n := atomic.LoadInt32(&deletes); n != 0 {
		t.Fatalf("append must delete nothing, got %d DELETEs", n)
	}
	var got struct {
		Body map[string]any `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if got.Body["appended"] != true {
		t.Errorf("--json should report the append, got:\n%s", out)
	}
}

// Every other append test drives `set`, so the create->append path never ran
// through a real command: upsert creates the row with POST /v1/pages and only
// then appends, and nothing pinned that the append still reaches Notion once a
// creation precedes it.
func TestUpsertAppendFileOnACreatedRowEndToEnd(t *testing.T) {
	var created bool
	var patched map[string]any
	var deletes int32
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			// No match: upsert must create rather than update.
			w.Write([]byte(`{"results":[],"has_more":false}`))
		case r.URL.Path == "/v1/pages" && r.Method == http.MethodPost:
			created = true
			w.Write([]byte(cliRowJSON))
		case r.Method == http.MethodDelete:
			atomic.AddInt32(&deletes, 1)
			fmt.Fprint(w, `{}`)
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/markdown"):
			_ = json.NewDecoder(r.Body).Decode(&patched)
			fmt.Fprint(w, `{"object":"page_markdown","id":"page1","markdown":"note",
				"truncated":false,"unknown_block_ids":[]}`)
		default:
			w.Write([]byte(cliRowJSON))
		}
	})

	note := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(note, []byte("## Progress\nCreated then appended."), 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"upsert", "--ticket", "BDF-231",
			"--append-file", note, "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	if !created {
		t.Fatal("upsert did not create the row")
	}
	if patched == nil {
		t.Fatal("the append never reached Notion after the create")
	}
	if patched["type"] != "insert_content" {
		t.Errorf(`type = %v, want "insert_content"`, patched["type"])
	}
	if n := atomic.LoadInt32(&deletes); n != 0 {
		t.Fatalf("an append must delete nothing, got %d DELETEs", n)
	}
	var got struct {
		Body map[string]any `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if got.Body["appended"] != true {
		t.Errorf("--json must report the append, got:\n%s", out)
	}
}

// append_bytes is public API -- scripts read it out of --json --dry-run -- and
// was only ever asserted through the human-readable line, which is free to be
// reworded.
func TestUpsertDryRunJSONExposesAppendBytes(t *testing.T) {
	cfg := withStubbedAPI(t, dryRunAPI(t, cliRowJSON))
	note := filepath.Join(t.TempDir(), "note.md")
	const content = "Ticket closed."
	if err := os.WriteFile(note, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"upsert", "--ticket", "BDF-231",
			"--append-file", note, "--dry-run", "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	var got struct {
		DryRun bool `json:"dry_run"`
		Plan   struct {
			AppendBytes int `json:"append_bytes"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if !got.DryRun {
		t.Errorf("dry_run must be true, got:\n%s", out)
	}
	if got.Plan.AppendBytes != len(content) {
		t.Errorf("plan.append_bytes = %d, want %d (the bytes that would be appended):\n%s",
			got.Plan.AppendBytes, len(content), out)
	}
}

// The pre-flight cap on --append-file, which had no test at all: without it an
// oversized file is only refused by Notion, after the request goes out.
func TestSetAppendFileOverTheLimitMakesNoHTTPCall(t *testing.T) {
	var calls int32
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/data_sources/ds1" {
			atomic.AddInt32(&calls, 1)
		}
		w.Write([]byte(cliSchemaJSON))
	})

	big := filepath.Join(t.TempDir(), "big.md")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), maxAppendFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}

	if code := executeArgs([]string{"set", "--ticket", "BDF-231",
		"--append-file", big, "--config", cfg}); code != ExitUsage {
		t.Fatalf("an oversized append file must exit %d, got %d", ExitUsage, code)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("the file must be rejected before any write reaches Notion, got %d calls", n)
	}
}

// The check must measure the REQUEST, not the file. JSON escaping inflates by
// a proportion rather than a constant — every newline becomes two bytes — so a
// file of short lines (a changelog, a bullet list) can sit under any
// file-sized cap and still serialize past Notion's payload limit. A fixed
// margin cannot cover that; this is why the earlier 450KB file cap was wrong.
func TestAppendRejectsFileWhoseEscapedPayloadExceedsTheLimit(t *testing.T) {
	// Just under the payload limit on disk, but half its bytes are newlines.
	lines := (maxAppendPayloadBytes - 1024) / 2
	raw := bytes.Repeat([]byte("a\n"), lines)
	if len(raw) > maxAppendPayloadBytes {
		t.Fatalf("test premise: %d bytes is not under the limit on disk", len(raw))
	}
	if appendPayloadBytes(string(raw)) <= maxAppendPayloadBytes {
		t.Skip("escaping no longer inflates this shape; the risk this pins is gone")
	}

	p := filepath.Join(t.TempDir(), "lines.md")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadAppendBody(p, nil, nil, nil)
	if err == nil {
		t.Fatalf("a %d-byte file serializing to %d bytes must be refused, not sent",
			len(raw), appendPayloadBytes(string(raw)))
	}
	if exitCodeFor(err) != ExitUsage {
		t.Fatalf("want a usage error, got %v (code %d)", err, exitCodeFor(err))
	}
	// The number quoted has to be the payload, or it reads as an arithmetic
	// error: "this 490000-byte file is over the 500000-byte limit".
	if !strings.Contains(err.Error(), "request") {
		t.Errorf("the message should say it is the request that is too large, got: %v", err)
	}
}

// --expand runs after the file is read and only ever grows the text, so the
// size that matters is the one after expansion. Checking only before would
// declare a file legal and let Notion refuse the request it produces.
func TestAppendRejectsContentThatOnlyExceedsTheLimitAfterExpand(t *testing.T) {
	// "{{date}} " is 9 bytes on disk and 11 once expanded.
	const unit = "{{date}} "
	raw := strings.Repeat(unit, (maxAppendPayloadBytes-2048)/len(unit))
	if appendPayloadBytes(raw) > maxAppendPayloadBytes {
		t.Fatalf("test premise: the file is already over the limit before expanding")
	}

	p := filepath.Join(t.TempDir(), "expand.md")
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	// Without --expand it is legal...
	if _, err := loadAppendBody(p, nil, nil, nil); err != nil {
		t.Fatalf("the unexpanded file is under the limit and must be accepted: %v", err)
	}
	// ...and with it, the same file crosses the limit and must be refused.
	_, err := loadAppendBody(p, nil, nil, map[string]string{"date": "2026-08-06"})
	if err == nil {
		t.Fatal("expansion grew the content past the limit and it was accepted anyway")
	}
	if exitCodeFor(err) != ExitUsage {
		t.Fatalf("want a usage error, got %v (code %d)", err, exitCodeFor(err))
	}
}

// A file the append path refuses must still be accepted by --body-file, or the
// two caps are not really doing different jobs. Inherited gap: --body-file's
// own limit had no test either.
func TestSetBodyFileOverTheLimitMakesNoHTTPCall(t *testing.T) {
	var calls int32
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/data_sources/ds1" {
			atomic.AddInt32(&calls, 1)
		}
		w.Write([]byte(cliSchemaJSON))
	})

	big := filepath.Join(t.TempDir(), "big.md")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), maxBodyFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}

	if code := executeArgs([]string{"set", "--ticket", "BDF-231",
		"--body-file", big, "--config", cfg}); code != ExitUsage {
		t.Fatalf("an oversized body file must exit %d, got %d", ExitUsage, code)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("the file must be rejected before any write reaches Notion, got %d calls", n)
	}
}

// The behaviour the two caps exist to produce: a file between them is a legal
// --body-file (parsed and batched) and an illegal --append-file (one payload
// over Notion's 500KB limit). Asserting the constants alone would not catch a
// wiring mistake that reads the wrong one.
func TestFileBetweenTheTwoCapsIsBodyOnlyNotAppend(t *testing.T) {
	between := bytes.Repeat([]byte("x"), maxAppendPayloadBytes+1)
	if len(between) > maxBodyFileBytes {
		t.Fatalf("test premise broken: %d bytes is not between the two caps", len(between))
	}
	if appendPayloadBytes(string(between)) <= maxAppendPayloadBytes {
		t.Fatalf("test premise broken: this content is a legal append payload")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "between.md")
	if err := os.WriteFile(file, between, 0o600); err != nil {
		t.Fatal(err)
	}

	// Rejected as an append, before any request goes out.
	var appendCalls int32
	appendCfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/data_sources/ds1" {
			atomic.AddInt32(&appendCalls, 1)
		}
		w.Write([]byte(cliSchemaJSON))
	})
	if code := executeArgs([]string{"set", "--ticket", "BDF-231",
		"--append-file", file, "--config", appendCfg}); code != ExitUsage {
		t.Errorf("a file over the append cap must exit %d, got %d", ExitUsage, code)
	}
	if n := atomic.LoadInt32(&appendCalls); n != 0 {
		t.Errorf("nothing should have reached Notion, got %d calls", n)
	}

	// Accepted as a body: the same bytes, batched rather than sent whole.
	bodyCfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case strings.HasSuffix(r.URL.Path, "/children") && r.Method == http.MethodGet:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		case strings.HasSuffix(r.URL.Path, "/children"):
			w.Write([]byte(`{"results":[],"has_more":false}`))
		case r.Method == http.MethodPatch:
			w.Write([]byte(cliRowJSON))
		default:
			w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
		}
	})
	// Asserted as ExitOK, not merely "not ExitUsage": the point is that the body
	// path really writes this file, and a regression to any other non-zero exit
	// would slip past the weaker check.
	if code := executeArgs([]string{"set", "--ticket", "BDF-231",
		"--body-file", file, "--config", bodyCfg}); code != ExitOK {
		t.Errorf("the same file must be a legal --body-file and write cleanly: got exit %d", code)
	}
}

// The unit-level counterpart to TestLoadBodyRejectsFileOverOneMiB, which only
// ever covered the body path. The message has to name the limit and say the
// size quoted is the request, not the file, or a user comparing it against
// `ls -l` reads it as an arithmetic error.
func TestLoadAppendBodyRejectsFileOverTheAppendCap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "huge.md")
	if err := os.WriteFile(p, bytes.Repeat([]byte("a"), maxAppendFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadAppendBody(p, nil, nil, nil)
	if err == nil {
		t.Fatal("want an error for an append file over the cap, got nil")
	}
	if exitCodeFor(err) != ExitUsage {
		t.Fatalf("an over-limit append file must be a usage error, got %v (code %d)", err, exitCodeFor(err))
	}
	for _, want := range []string{"request", "payload"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message should mention %q, got: %v", want, err)
		}
	}
}

// The largest content that still fits must be accepted: the check is > and not
// >=, and an off-by-one here silently costs a KB of legal content. Sized by
// measuring the payload rather than the file, since the two differ.
func TestLoadAppendBodyAcceptsContentAtExactlyThePayloadLimit(t *testing.T) {
	// Binary-search the longest plain-'a' content whose payload is exactly at
	// the limit. Plain 'a' needs no escaping, so the envelope is the only
	// overhead and the search converges immediately.
	lo, hi := 0, maxAppendPayloadBytes
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if appendPayloadBytes(strings.Repeat("a", mid)) <= maxAppendPayloadBytes {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	content := strings.Repeat("a", lo)
	if got := appendPayloadBytes(content); got != maxAppendPayloadBytes {
		t.Logf("largest fitting payload is %d bytes (limit %d)", got, maxAppendPayloadBytes)
	}

	p := filepath.Join(t.TempDir(), "atcap.md")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	req, err := loadAppendBody(p, nil, nil, nil)
	if err != nil {
		t.Fatalf("content whose payload is exactly at the limit must be accepted: %v", err)
	}
	if len(req.AppendMarkdown) != len(content) {
		t.Errorf("content = %d bytes, want %d", len(req.AppendMarkdown), len(content))
	}
	// One byte more must not fit, or the boundary is not where we think.
	p2 := filepath.Join(t.TempDir(), "overcap.md")
	if err := os.WriteFile(p2, []byte(content+"a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAppendBody(p2, nil, nil, nil); err == nil {
		t.Error("one byte past the limit must be refused")
	}
}

// A BOM-only file is what an editor on Windows writes for an empty file saved
// as UTF-8-with-BOM. U+FEFF left Unicode's White_Space in 6.3, so TrimSpace
// keeps it and the file reads as content — which Notion answers 200 to and does
// nothing about, reporting a success for a page that did not change. The silent
// no-op the empty guard exists to prevent, arriving through the guard.
func TestAppendRejectsAFileHoldingOnlyInvisibleCharacters(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"a bare BOM", "\ufeff"},
		{"a BOM with whitespace", "\ufeff  \n\t\n"},
		{"a zero-width space", "\u200b"},
		{"several BOMs, as concatenation leaves", "\ufeff\ufeff\ufeff"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "blank.md")
			if err := os.WriteFile(p, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := loadAppendBody(p, nil, nil, nil)
			if err == nil {
				t.Fatal("accepted as content; Notion would 200 and change nothing")
			}
			if exitCodeFor(err) != ExitUsage {
				t.Fatalf("want a usage error, got %v (code %d)", err, exitCodeFor(err))
			}
		})
	}
}

// The same guard after --expand: a BOM can be left stranded once the text
// around it resolves to nothing.
func TestAppendRejectsContentThatExpandsToOnlyABOM(t *testing.T) {
	p := filepath.Join(t.TempDir(), "expand.md")
	if err := os.WriteFile(p, []byte("\ufeff{{ticket}}"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadAppendBody(p, nil, nil, map[string]string{"ticket": ""})
	if err == nil {
		t.Fatal("content that expands to nothing but a BOM must be refused")
	}
	if exitCodeFor(err) != ExitUsage {
		t.Fatalf("want a usage error, got %v (code %d)", err, exitCodeFor(err))
	}
}

// A BOM in front of real content is normal and must not be mistaken for blank.
func TestAppendKeepsAFileWhoseBOMPrefixesRealContent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bom-content.md")
	if err := os.WriteFile(p, []byte("\ufeff## Progress\nShipped.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	req, err := loadAppendBody(p, nil, nil, nil)
	if err != nil {
		t.Fatalf("a BOM before real content must not make the file blank: %v", err)
	}
	// The BOM is not stripped from what gets sent: only the emptiness TEST
	// ignores it. Notion renders the content either way, and rewriting a user's
	// bytes is not this function's job.
	if !strings.Contains(req.AppendMarkdown, "## Progress") {
		t.Errorf("content = %q", req.AppendMarkdown)
	}
}

// The cheap pre-gate speaks in FILE terms, because it has measured no request.
// readBodySource stops one byte past the larger cap, so for a file bigger than
// that len(raw) is the read ceiling — neither the file nor the request. Quoting
// it as "builds an N-byte request" names a number that is neither, and an agent
// halving it gets two more refusals.
func TestPreGateMessageDoesNotClaimAMeasuredRequestSize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "huge.md")
	if err := os.WriteFile(p, bytes.Repeat([]byte("a"), 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadAppendBody(p, nil, nil, nil)
	if err == nil {
		t.Fatal("a 2 MiB append file must be refused")
	}
	msg := err.Error()
	// The read ceiling must never be presented as a size of anything real.
	if strings.Contains(msg, "builds a 1048577-byte request") {
		t.Errorf("quotes the read ceiling as a request size: %v", err)
	}
	// It must still say the file is too large and name the real limit.
	if !strings.Contains(msg, "at least") {
		t.Errorf("the pre-gate should hedge the size it did not measure, got: %v", err)
	}
	if !strings.Contains(msg, strconv.Itoa(maxAppendPayloadBytes)) {
		t.Errorf("the message should name Notion's limit, got: %v", err)
	}
}

// --body-file REPLACES: the new blocks are appended, then every old one is
// deleted. loadBody's empty guard is the only thing standing between a file
// with no content and a body wipe — and strings.TrimSpace does not strip
// U+FEFF, which left Unicode's White_Space in 6.3. A BOM-only file therefore
// reads as content, goldmark returns zero blocks, ValidateAppendable returns
// nil on the empty slice, splitIntoRequests emits no batch so no PATCH goes
// out, and replaceBody deletes every child anyway. The body is gone, nothing
// replaced it, and the run exits 0. See #37.
func TestSetBodyFileHoldingOnlyInvisibleCharactersMakesNoHTTPCall(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"a bare BOM", "\ufeff"},
		{"a BOM with whitespace", "\ufeff  \n\t\n"},
		{"a zero-width space", "\u200b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/data_sources/ds1" {
					atomic.AddInt32(&calls, 1)
				}
				w.Write([]byte(cliSchemaJSON))
			})

			p := filepath.Join(t.TempDir(), "blank.md")
			if err := os.WriteFile(p, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}

			if code := executeArgs([]string{"set", "--ticket", "BDF-231",
				"--body-file", p, "--config", cfg}); code != ExitUsage {
				t.Fatalf("a body file with no content must exit %d, got %d", ExitUsage, code)
			}
			if n := atomic.LoadInt32(&calls); n != 0 {
				t.Fatalf("it must be refused before anything reaches Notion, got %d calls", n)
			}
		})
	}
}

// The overcorrection this fix must not make: a BOM in front of real content is
// an ordinary file, and refusing it would break writing bodies from any editor
// that emits one.
func TestBodyFileKeepsAFileWhoseBOMPrefixesRealContent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bom-content.md")
	if err := os.WriteFile(p, []byte("\ufeff## Progress\nShipped.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	req, _, err := loadBody(p, nil, nil, nil)
	if err != nil {
		t.Fatalf("a BOM before real content must not make the file blank: %v", err)
	}
	if len(req.Blocks) == 0 {
		t.Fatal("the content parsed to no blocks at all")
	}
}

// The twin hole, on the same replace path: --expand can empty a file that was
// non-blank on disk. loadAppendBody re-checks after expanding; loadBody did
// not, so a file of nothing but "{{ticket}}" addressed by --page-id parsed to
// zero blocks and the body was deleted with nothing to replace it.
func TestBodyFileThatExpandsToNothingIsRefused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "expand.md")
	if err := os.WriteFile(p, []byte("\ufeff{{ticket}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := loadBody(p, nil, nil, map[string]string{"ticket": ""})
	if err == nil {
		t.Fatal("a body file that expands to nothing must be refused")
	}
	if exitCodeFor(err) != ExitUsage {
		t.Fatalf("want a usage error, got %v (code %d)", err, exitCodeFor(err))
	}
}
