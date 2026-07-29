package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// stubForList answers schema and query, keeping the filter of the last query.
func stubForList(t *testing.T, profile string, sent *map[string]any) string {
	t.Helper()
	return withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		var body struct {
			Filter map[string]any `json:"filter"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		*sent = body.Filter
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	}, profile)
}

func TestListFiltersByAssignee(t *testing.T) {
	var sent map[string]any
	cfg := stubForList(t, assigneeProfile, &sent)

	captureStdout(t, func() {
		if code := executeArgs([]string{
			"list", "--assignee", "mirko", "--config", cfg,
		}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	if sent["property"] != "Referente" {
		t.Fatalf("filter = %#v, want it on the Referente column", sent)
	}
	if got := sent["select"].(map[string]any)["equals"]; got != "Mirko Spinato" {
		t.Errorf("filter value = %v, want the canonical option", got)
	}
}

func TestListUnassigned(t *testing.T) {
	var sent map[string]any
	cfg := stubForList(t, assigneeProfile, &sent)

	captureStdout(t, func() {
		executeArgs([]string{"list", "--unassigned", "--config", cfg})
	})

	if got := sent["select"].(map[string]any)["is_empty"]; got != true {
		t.Errorf("filter = %#v, want is_empty", sent)
	}
}

func TestListAssigneeAndUnassignedAreExclusive(t *testing.T) {
	var sent map[string]any
	cfg := stubForList(t, assigneeProfile, &sent)

	if code := executeArgs([]string{
		"list", "--assignee", "mirko", "--unassigned", "--config", cfg,
	}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}

func TestListHumanRowsShowTheAssignee(t *testing.T) {
	var sent map[string]any
	cfg := stubForList(t, assigneeProfile, &sent)

	out := captureStdout(t, func() {
		executeArgs([]string{"list", "--config", cfg})
	})

	if !strings.Contains(out, "@Mirko Spinato") {
		t.Errorf("output = %q, want the assignee in the row", out)
	}
}

func TestListHumanRowsAreUnchangedWithoutTheRole(t *testing.T) {
	// Non-regression on the row format: the columns must not shift for a
	// profile that never mapped the role.
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})

	out := captureStdout(t, func() {
		executeArgs([]string{"list", "--config", cfg})
	})

	// The row format as it stood before the assignee feature: it must not shift.
	want := "BDF-231              Hardening                                [Fatto]\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestListFiltersByPriorityFlag(t *testing.T) {
	var sent map[string]any
	cfg := stubForList(t, assigneeProfile, &sent)

	captureStdout(t, func() {
		if code := executeArgs([]string{
			"list", "--priority", "alta", "--config", cfg,
		}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	if sent["property"] != "Urgenza" {
		t.Fatalf("filter = %#v, want it on Urgenza", sent)
	}
}

func TestListRowsShowThePriority(t *testing.T) {
	// sent only satisfies stubForList's signature; this test asserts on the
	// printed row, not on the filter that produced it.
	var sent map[string]any
	cfg := stubForList(t, assigneeProfile, &sent)

	out := captureStdout(t, func() {
		executeArgs([]string{"list", "--config", cfg})
	})

	// Asserts the adjacent pair, not just "!ALTA" in isolation: that weaker
	// assertion stayed green even after swapping the priority and assignee
	// arguments in list.go's two Printf calls, which prints the row as
	// "@Mirko Spinato  !ALTA" instead of spec §5's required order.
	if !strings.Contains(out, "[Fatto]  !ALTA  @Mirko Spinato") {
		t.Errorf("output = %q, want the priority before the assignee", out)
	}
}

func TestListPrintsTheBoardIDForHumans(t *testing.T) {
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaWithIDJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowWithIDJSON + `],"has_more":false}`))
	}, idProfileYAML)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"list", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	// The id leads the row: names read down the left edge of a list.
	if !strings.HasPrefix(out, "BDF-271") {
		t.Errorf("output = %q, want the row to start with the board id", out)
	}
}
