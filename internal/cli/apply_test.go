package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeManifest drops a manifest in its own directory and returns its path.
func writeManifest(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// applyAPI answers reads, and lets the test decide what a write returns and
// how many it saw.
func applyAPI(queryResults string, writes *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + queryResults + `],"has_more":false}`))
		default:
			if writes != nil {
				*writes++
			}
			w.Write([]byte(cliRowJSON))
		}
	}
}

func TestApplyRunsEveryEntry(t *testing.T) {
	var writes int
	cfg := withStubbedAPI(t, applyAPI(cliRowJSON, &writes))
	m := writeManifest(t, "tasks.json", `[
		{"op":"upsert","ticket":"BDF-1","status":"Fatto"},
		{"op":"set","ticket":"BDF-231","status":"Fatto"}
	]`)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"apply", "--file", m, "--config", cfg}); code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	if writes != 2 {
		t.Errorf("%d writes, want one per entry", writes)
	}
	for _, want := range []string{"1/2", "2/2", "BDF-1", "BDF-231"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not report %q:\n%s", want, out)
		}
	}
}

func TestApplyReadsCSV(t *testing.T) {
	var writes int
	cfg := withStubbedAPI(t, applyAPI(cliRowJSON, &writes))
	m := writeManifest(t, "tasks.csv", "op,ticket,status\nupsert,BDF-1,Fatto\nset,BDF-231,Fatto\n")

	captureStdout(t, func() {
		if code := executeArgs([]string{"apply", "--file", m, "--config", cfg}); code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	if writes != 2 {
		t.Errorf("%d writes, want 2", writes)
	}
}

// Fail-fast is the issue's explicit requirement: stop at the first failure
// rather than plowing through, and say where it stopped.
func TestApplyStopsAtTheFirstFailure(t *testing.T) {
	var writes int
	// The second entry sets a status the schema does not offer, which fails
	// before any request is made for it.
	cfg := withStubbedAPI(t, applyAPI(cliRowJSON, &writes))
	m := writeManifest(t, "tasks.json", `[
		{"ticket":"BDF-1","status":"Fatto"},
		{"ticket":"BDF-2","status":"Nonexistent"},
		{"ticket":"BDF-3","status":"Fatto"}
	]`)

	var code int
	out := captureStdout(t, func() {
		code = executeArgs([]string{"apply", "--file", m, "--config", cfg})
	})

	if code == ExitOK {
		t.Fatal("apply exited 0 despite a failing entry")
	}
	if writes != 1 {
		t.Errorf("%d writes, want only the first entry applied", writes)
	}
	if !strings.Contains(out, "2/3") || !strings.Contains(out, "failed") {
		t.Errorf("output does not report the failure:\n%s", out)
	}
	// The third entry must not appear at all: it was never attempted.
	if strings.Contains(out, "BDF-3") {
		t.Errorf("output mentions an entry that was never attempted:\n%s", out)
	}
}

// The exit code has to survive the batch: a pipeline branching on 3 or 4
// still needs to learn why the run stopped.
func TestApplyExitsWithTheFailingEntrysCode(t *testing.T) {
	cfg := withStubbedAPI(t, applyAPI("", nil)) // no rows: set cannot find its ticket
	m := writeManifest(t, "tasks.json", `[{"op":"set","ticket":"NOPE","status":"Fatto"}]`)

	var code int
	captureStdout(t, func() {
		code = executeArgs([]string{"apply", "--file", m, "--config", cfg})
	})

	if code != ExitNotFound {
		t.Fatalf("exit code = %d, want %d (not found)", code, ExitNotFound)
	}
}

func TestApplyJSONReportsEveryEntry(t *testing.T) {
	cfg := withStubbedAPI(t, applyAPI(cliRowJSON, nil))
	m := writeManifest(t, "tasks.json", `[{"ticket":"BDF-1"},{"ticket":"BDF-2"}]`)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"apply", "--file", m, "--json", "--config", cfg}); code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	if got["applied"] != float64(2) || got["total"] != float64(2) {
		t.Errorf("applied/total = %v/%v", got["applied"], got["total"])
	}
	entries, _ := got["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %v", got["entries"])
	}
}

// A partial run's JSON has to carry the counts too, or a script cannot tell
// how far it got.
func TestApplyJSONReportsAPartialRun(t *testing.T) {
	cfg := withStubbedAPI(t, applyAPI(cliRowJSON, nil))
	m := writeManifest(t, "tasks.json", `[
		{"ticket":"BDF-1"},
		{"ticket":"BDF-2","status":"Nonexistent"}
	]`)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"apply", "--file", m, "--json", "--config", cfg}); code == ExitOK {
			t.Error("exit code 0 on a failed entry")
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	if got["applied"] != float64(1) {
		t.Errorf("applied = %v, want 1", got["applied"])
	}
	entries, _ := got["entries"].([]any)
	last, _ := entries[len(entries)-1].(map[string]any)
	if last["error"] == nil {
		t.Errorf("the failing entry carries no error: %v", last)
	}
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	var writes int
	cfg := withStubbedAPI(t, applyAPI(cliRowJSON, &writes))
	m := writeManifest(t, "tasks.json", `[{"ticket":"BDF-1","status":"Fatto"}]`)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{
			"apply", "--file", m, "--dry-run", "--config", cfg,
		}); code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	if writes != 0 {
		t.Errorf("%d writes on a dry run", writes)
	}
	if !strings.Contains(out, "would be") {
		t.Errorf("output does not report a plan:\n%s", out)
	}
}

// A body_file is resolved against the manifest, not the working directory, so
// a manifest and the files it names travel together.
func TestApplyResolvesBodyFilesRelativeToTheManifest(t *testing.T) {
	var writes int
	cfg := withStubbedAPI(t, applyAPI(cliRowJSON, &writes))
	dir := t.TempDir()
	m := filepath.Join(dir, "tasks.json")
	if err := os.WriteFile(m, []byte(`[{"ticket":"BDF-1","body_file":"notes.md"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# Notes\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	captureStdout(t, func() {
		if code := executeArgs([]string{"apply", "--file", m, "--config", cfg}); code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	if writes == 0 {
		t.Error("nothing was written, so the body file was never found")
	}
}

func TestApplyRejectsAMalformedManifestAsAUsageError(t *testing.T) {
	cfg := withStubbedAPI(t, applyAPI(cliRowJSON, nil))
	m := writeManifest(t, "tasks.json", `[{"ticket":"BDF-1","stuats":"Fatto"}]`)

	if code := executeArgs([]string{"apply", "--file", m, "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (usage)", code, ExitUsage)
	}
}

// The manifest is validated before the service is built, so a broken file
// fails the same way whether or not the profile or token are in order.
func TestApplyValidatesTheManifestBeforeTouchingAnything(t *testing.T) {
	m := writeManifest(t, "tasks.yaml", "- ticket: BDF-1\n")

	if code := executeArgs([]string{"apply", "--file", m, "--config", "/nonexistent/config.yml"}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (usage)", code, ExitUsage)
	}
}

func TestApplyRequiresAFile(t *testing.T) {
	if code := executeArgs([]string{"apply"}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (usage)", code, ExitUsage)
	}
}

func TestApplyWritesTheAssignee(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "tasks.csv")
	os.WriteFile(manifestPath, []byte("op,ticket,unassign\nset,BDF-231,true\n"), 0o600)

	captureStdout(t, func() {
		if code := executeArgs([]string{
			"apply", "--file", manifestPath, "--config", cfg,
		}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	got, _ := json.Marshal(written["Referente"])
	if want := `{"select":null}`; string(got) != want {
		t.Errorf("Referente = %s, want %s", got, want)
	}
}

func TestApplyRejectsSettingAndClearingTheSameEntry(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "tasks.csv")
	os.WriteFile(manifestPath, []byte("op,ticket,assignee,unassign\nset,BDF-231,mirko,true\n"), 0o600)

	captureStdout(t, func() {
		// Exit 2, not 1: apply never touches cobra, so the typed error is the
		// only thing carrying the usage verdict out of the domain layer.
		if code := executeArgs([]string{
			"apply", "--file", manifestPath, "--config", cfg,
		}); code != ExitUsage {
			t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
		}
	})
	if written != nil {
		t.Errorf("a rejected entry still wrote %v", written)
	}
}
