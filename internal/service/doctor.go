package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

// Check is one diagnostic result. Status is "ok", "warn" or "fail".
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Doctor verifies token, access, schema drift and duplicate tickets.
//
// It never stops at the first failure: when something is broken the user wants
// the whole picture, not one symptom at a time.
func (s *Service) Doctor(ctx context.Context) []Check {
	var checks []Check

	name, err := s.client.Me(ctx)
	if err != nil {
		return append(checks, Check{"token", "fail", fmt.Sprintf(
			"%v\n  fix: check NOTION_TOKEN, or create a new internal integration token in Notion", err)})
	}
	checks = append(checks, Check{"token", "ok", "authenticated as " + name})

	schema, err := s.Schema(ctx)
	if err != nil {
		return append(checks, Check{"data_source", "fail", fmt.Sprintf(
			"cannot read data source %s: %v\n"+
				"  fix: open the database in Notion → ••• → Connections → add your integration",
			s.profile.DataSourceID, err)})
	}
	checks = append(checks, Check{"data_source", "ok", "reachable: " + schema.Title})

	checks = append(checks, s.checkProperties(schema))
	checks = append(checks, s.checkDuplicates(ctx))
	return checks
}

// checkProperties compares the configured mapping against the live schema and,
// when a property is gone, names the most likely replacement.
func (s *Service) checkProperties(schema *notion.Schema) Check {
	mapped := map[string]string{
		"ticket": s.profile.Properties.Ticket,
		"status": s.profile.Properties.Status,
		"title":  s.profile.Properties.Title,
		"due":    s.profile.Properties.Due,
	}
	wantType := map[string][]string{
		"ticket": {"rich_text", "title"},
		"status": {"status", "select"},
		"title":  {"title"},
		"due":    {"date"},
	}

	var problems []string
	roles := []string{"ticket", "status", "title", "due"}
	for _, role := range roles {
		name := mapped[role]
		if name == "" {
			continue
		}
		prop, ok := schema.Properties[name]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"%s property %q no longer exists%s", role, name, suggest(schema, wantType[role])))
			continue
		}
		if !containsString(wantType[role], prop.Type) {
			problems = append(problems, fmt.Sprintf(
				"%s property %q has type %q, expected one of %s",
				role, name, prop.Type, strings.Join(wantType[role], ", ")))
		}
	}

	// status_type records what the status property was when init ran. A change
	// here is not fatal but it flips the safety model: a select silently
	// creates unknown options, a status rejects them.
	if want := s.profile.StatusType; want != "" {
		if got, ok := schema.Properties[s.profile.Properties.Status]; ok && got.Type != want {
			problems = append(problems, fmt.Sprintf(
				"status property changed type from %q to %q since init; "+
					"re-run 'notion-track init' to record it", want, got.Type))
		}
	}

	if len(problems) == 0 {
		return Check{"properties", "ok", "all mapped properties exist with the expected types"}
	}
	return Check{"properties", "fail", strings.Join(problems, "\n  ") +
		"\n  fix: run 'notion-track init' to remap, or rename the property back in Notion"}
}

// suggest names the columns that could stand in for a missing property.
func suggest(schema *notion.Schema, types []string) string {
	var candidates []string
	for name, p := range schema.Properties {
		if containsString(types, p.Type) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Strings(candidates)
	return "; candidates of the right type: " + strings.Join(candidates, ", ")
}

// checkDuplicates scans every row for repeated ticket keys. Duplicates make
// upsert fail permanently, so this is the command that tells you which rows to
// go delete.
func (s *Service) checkDuplicates(ctx context.Context) Check {
	pages, err := s.client.QueryPages(ctx, s.profile.DataSourceID, nil)
	if err != nil {
		return Check{"duplicates", "warn", fmt.Sprintf("could not scan rows: %v", err)}
	}

	byTicket := map[string][]notion.Page{}
	prop := s.profile.Properties.Ticket
	for _, p := range pages {
		if key := p.Properties[prop].Text; key != "" {
			byTicket[key] = append(byTicket[key], p)
		}
	}

	var keys []string
	for key, rows := range byTicket {
		if len(rows) > 1 {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return Check{"duplicates", "ok", fmt.Sprintf("%d rows, no repeated ticket keys", len(pages))}
	}

	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "%d ticket keys appear more than once:", len(keys))
	for _, key := range keys {
		fmt.Fprintf(&b, "\n  %s", key)
		for _, p := range byTicket[key] {
			fmt.Fprintf(&b, "\n    %s", p.URL)
		}
	}
	b.WriteString("\n  fix: delete the extra rows in Notion; upsert refuses to guess between them")
	return Check{"duplicates", "fail", b.String()}
}

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
