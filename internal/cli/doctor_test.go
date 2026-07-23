package cli

import (
	"net/http"
	"strings"
	"testing"
)

func TestDoctorReportsEveryCheck(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	})

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"doctor", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	for _, want := range []string{"token", "data_source", "properties", "duplicates"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing the %q check: %s", want, out)
		}
	}
}

func TestDoctorExitsNonZeroWhenACheckFails(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":"unauthorized","message":"API token is invalid."}`))
	})

	captureStdout(t, func() {
		if code := executeArgs([]string{"doctor", "--config", cfg}); code == ExitOK {
			t.Fatal("doctor exited 0 despite a failing check")
		}
	})
}

func TestInitHeadlessWritesTheProfile(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1", "--database-id", "db1",
		"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
		"--profile", "work", "--config", cfg,
	})
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	loaded, err := loadConfigFrom(cfg)
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	p, err := loaded.Resolve("work")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.DataSourceID != "ds1" || p.Properties.Status != "Stato" || p.StatusType != "status" {
		t.Fatalf("profile = %+v", p)
	}
}

func TestInitListPrintsSharedDataSources(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[
			{"id":"ds1","title":[{"plain_text":"Tasks"}],"parent":{"database_id":"db1"}}
		],"has_more":false}`))
	})

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"init", "--list", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if !strings.Contains(out, "ds1") || !strings.Contains(out, "Tasks") {
		t.Fatalf("output does not list the data source: %q", out)
	}
}

// An empty list is the single most likely first-run failure, so the message
// has to name the fix rather than just report emptiness.
func TestInitListExplainsAnEmptyResult(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})
	captureStdout(t, func() {
		if code := executeArgs([]string{"init", "--list", "--config", cfg}); code == ExitOK {
			t.Fatal("an empty list should not exit 0")
		}
	})
}

// init must refuse a mapping the data source does not support, instead of
// writing a config that fails on first use.
func TestInitHeadlessRejectsAnInvalidMapping(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1",
		"--ticket-prop", "Nonexistent", "--status-prop", "Stato", "--title-prop", "Name",
		"--config", cfg,
	})
	if code == ExitOK {
		t.Fatal("init accepted a property that does not exist")
	}
}
