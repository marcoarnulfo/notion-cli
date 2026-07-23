package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/config"
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
	_, _, err := loadBody(p, nil, nil)
	if err == nil || exitCodeFor(err) != ExitUsage {
		t.Fatalf("empty file must be a usage error, got %v (code %d)", err, exitCodeFor(err))
	}
}

func TestLoadBodyRejectsMissingFile(t *testing.T) {
	_, _, err := loadBody(filepath.Join(t.TempDir(), "nope.md"), nil, nil)
	if err == nil || exitCodeFor(err) != ExitUsage {
		t.Fatalf("missing file must be a usage error, got %v", err)
	}
}

func TestLoadBodyReadsStdin(t *testing.T) {
	req, _, err := loadBody("-", strings.NewReader("# Title\n\nbody\n"), nil)
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
