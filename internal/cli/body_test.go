package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
