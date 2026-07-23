package cli

import (
	"net/http"
	"testing"
)

func TestUpsertCreatesAndIsQuietOnSuccess(t *testing.T) {
	var created bool
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[],"has_more":false}`))
		case "/v1/pages":
			created = true
			w.Write([]byte(cliRowJSON))
		default:
			w.Write([]byte(cliRowJSON))
		}
	})

	out := captureStdout(t, func() {
		code := executeArgs([]string{"upsert", "--ticket", "BDF-231", "--status", "Fatto", "--config", cfg})
		if code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if !created {
		t.Fatal("no page was created")
	}
	// Quiet on success: CI logs stay readable, --json is the opt-in.
	if out != "" {
		t.Fatalf("expected no output, got %q", out)
	}
}

// cmd.MarkFlagRequired("ticket") only checks that --ticket was passed, not
// that it carries a value, so `--ticket ""` used to exit 0 and create a row
// whose payload never carries the ticket property at all — a ghost row that
// no future get/set/upsert can ever reach again. A CI script with an
// unset $TICKET is exactly how this happens in practice.
func TestUpsertRejectsAnEmptyTicketBeforeCallingTheAPI(t *testing.T) {
	var wrote bool
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/pages" {
			wrote = true
		}
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})

	if code := executeArgs([]string{"upsert", "--ticket", "", "--title", "Ghost", "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
	if wrote {
		t.Fatal("an empty ticket reached the API; a ghost row was created")
	}
}

func TestUpsertRejectsAnUnknownStatusBeforeCallingTheAPI(t *testing.T) {
	var wrote bool
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/pages" {
			wrote = true
		}
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})

	// A rejected value is invalid usage, not a generic failure.
	if code := executeArgs([]string{"upsert", "--ticket", "X", "--status", "Fattto", "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if wrote {
		t.Fatal("a bogus status reached the API; a select property would have created it")
	}
}

func TestUpsertExitsDuplicateOnSeveralMatches(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `,` + cliRowJSON + `],"has_more":false}`))
	})

	if code := executeArgs([]string{"upsert", "--ticket", "BDF-231", "--config", cfg}); code != ExitDuplicate {
		t.Fatalf("exit code = %d, want %d", code, ExitDuplicate)
	}
}

// The exit-code table documents code 4 for upsert, set and get alike; set
// had no test pinning it, only upsert and get did.
func TestSetExitsDuplicateOnSeveralMatches(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `,` + cliRowJSON + `],"has_more":false}`))
	})

	if code := executeArgs([]string{"set", "--ticket", "BDF-231", "--status", "Fatto", "--config", cfg}); code != ExitDuplicate {
		t.Fatalf("exit code = %d, want %d", code, ExitDuplicate)
	}
}

func TestSetExitsNotFoundInsteadOfCreating(t *testing.T) {
	var created bool
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case "/v1/pages":
			created = true
			w.Write([]byte(cliRowJSON))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	})

	if code := executeArgs([]string{"set", "--ticket", "NOPE", "--status", "Fatto", "--config", cfg}); code != ExitNotFound {
		t.Fatalf("exit code = %d, want %d", code, ExitNotFound)
	}
	if created {
		t.Fatal("set created a row; only upsert may do that")
	}
}
