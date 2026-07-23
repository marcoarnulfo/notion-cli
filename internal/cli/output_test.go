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
