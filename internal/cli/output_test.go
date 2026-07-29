package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

func TestPrintJSONUsesSnakeCaseKeys(t *testing.T) {
	var buf bytes.Buffer
	if err := printJSON(&buf, map[string]string{"page_id": "page1"}); err != nil {
		t.Fatalf("printJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %s", buf.String())
	}
	if got["page_id"] != "page1" {
		t.Fatalf("got %v", got)
	}
}

// Deferred from Task 16: no command took a required flag until upsert did.
// A missing flag is invalid usage, not a generic failure.
func TestMissingRequiredFlagExitsUsage(t *testing.T) {
	if code := executeArgs([]string{"upsert"}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}

func TestExitCodeForMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"not found", fmt.Errorf("wrapped: %w", service.ErrNotFound), ExitNotFound},
		{"duplicates", &tracker.DuplicateError{Ticket: "X"}, ExitDuplicate},
		{"rejected value", &tracker.ValidationError{Field: "status", Value: "X"}, ExitUsage},
		{"unauthorized", fmt.Errorf("wrapped: %w", notion.ErrUnauthorized), ExitAuth},
		{"not configured", fmt.Errorf("wrapped: %w", config.ErrNotConfigured), ExitUsage},
		// An unreadable credentials.yml (bad permissions, a directory in its
		// place) used to fall through to the generic ExitError instead of
		// the ExitAuth every other missing/broken-token path already uses.
		{"credentials unreadable", fmt.Errorf("wrapped: %w", config.ErrCredentialsUnreadable), ExitAuth},
		// A corrupted credentials.yml (fails to parse as YAML) used to exit
		// the generic ExitError while an unreadable one exited ExitAuth, even
		// though the same reasoning applies to both: there may be a token in
		// there that nothing can prove exists.
		{"credentials invalid", fmt.Errorf("wrapped: %w", config.ErrInvalidCredentials), ExitAuth},
		{"empty ticket", fmt.Errorf("wrapped: %w", service.ErrEmptyTicket), ExitUsage},
		// A 400 from Notion is, by construction, a value the API rejected —
		// e.g. --due "yesterday" is not a valid ISO date. That is invalid
		// usage, not a generic failure, and must land in the same bucket as
		// tracker.ValidationError rather than the network/API catch-all.
		{"API rejects the value", &notion.APIError{Status: 400, Code: "validation_error", Message: "bad"}, ExitUsage},
		{"API server error", &notion.APIError{Status: 500, Code: "internal_server_error", Message: "boom"}, ExitError},
		{"anything else", errors.New("boom"), ExitError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCodeFor(tc.err); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestExitCodeForIDErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"malformed id", &tracker.InvalidIDError{Value: "x", Reason: "expected a number"}, ExitUsage},
		{"empty id", service.ErrEmptyID, ExitUsage},
		{"id role not mapped", service.ErrNoIDProperty, ExitUsage},
		// Wrapped the way the service actually returns it.
		{"wrapped not-mapped", fmt.Errorf("%w: run init", service.ErrNoIDProperty), ExitUsage},
		{"unknown id", fmt.Errorf("%w: BDF-999", service.ErrNotFound), ExitNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCodeFor(tt.err); got != tt.want {
				t.Errorf("exitCodeFor(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
