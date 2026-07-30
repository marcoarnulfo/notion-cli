package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

// Check is one diagnostic result. Status is "ok", "warn" or "fail".
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Doctor verifies token, access, schema drift and duplicate tickets.
//
// It never stops at a failure that leaves other checks still meaningful: when
// something is broken the user wants the whole picture, not one symptom at a
// time. The only exception is the token check: without a valid token no
// Notion API call can succeed, so a failure there ends the run immediately.
// A data source failure, by contrast, only takes properties down with it —
// checkDuplicates does not need the schema, so it still runs.
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
		checks = append(checks, Check{"data_source", "fail", fmt.Sprintf(
			"cannot read data source %s: %v\n"+
				"  fix: open the database in Notion → ••• → Connections → add your integration",
			s.profile.DataSourceID, err)})
	} else {
		checks = append(checks, Check{"data_source", "ok", "reachable: " + schema.Title})
		checks = append(checks, s.checkProperties(schema))
		if s.profile.Properties.Assignee != "" {
			checks = append(checks, s.checkAssignee(schema))
		}
	}

	checks = append(checks, s.checkDuplicates(ctx))
	return checks
}

// checkProperties compares the configured mapping against the live schema and,
// when a property is gone, names the most likely replacement.
func (s *Service) checkProperties(schema *notion.Schema) Check {
	mapped := map[string]string{
		"ticket":   s.profile.Properties.Ticket,
		"status":   s.profile.Properties.Status,
		"title":    s.profile.Properties.Title,
		"due":      s.profile.Properties.Due,
		"assignee": s.profile.Properties.Assignee,
		"priority": s.profile.Properties.Priority,
		"id":       s.profile.Properties.ID,
	}
	wantType := map[string][]string{
		"ticket":   {"rich_text", "title"},
		"status":   {"status", "select"},
		"title":    {"title"},
		"due":      {"date"},
		"assignee": {"select"},
		"priority": {"select"},
		"id":       {"unique_id"},
	}

	// optionalRoles may legitimately be unmapped; every other role is required
	// for notion-track to function at all, so leaving it blank is a fail, not a
	// silent skip — an empty mapped name otherwise makes every downstream
	// lookup key into "", which findByTicket and checkDuplicates would then
	// read as "nothing to report" instead of "not configured".
	//
	// A board may legitimately track nobody, so an unmapped assignee is a
	// skip, not a failure — the same judgement already made for due. A
	// priority is the same story again: not every board ranks urgency, and
	// there is no identity to resolve for it, so checkProperties existing
	// (column present, right type) is the whole check it needs. An id is the
	// fourth: it is a way to address a row, and a board without one is simply
	// addressed the other two ways.
	optionalRoles := map[string]bool{"due": true, "assignee": true, "priority": true, "id": true}

	var problems []string
	var warnings []string
	roles := []string{"ticket", "status", "title", "due", "assignee", "priority", "id"}
	for _, role := range roles {
		name := mapped[role]
		if name == "" {
			if optionalRoles[role] {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s property is not configured; run 'notion-track init' to map it", role))
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
	// creates unknown options, a status rejects them. Kept separate from
	// problems above so it can warn on its own instead of failing the whole
	// check when it is the only thing wrong.
	if want := s.profile.StatusType; want != "" {
		if got, ok := schema.Properties[s.profile.Properties.Status]; ok && got.Type != want {
			warnings = append(warnings, fmt.Sprintf(
				"status property changed type from %q to %q since init; "+
					"re-run 'notion-track init' to record it", want, got.Type))
		}
	}

	switch {
	case len(problems) > 0:
		detail := strings.Join(problems, "\n  ")
		if len(warnings) > 0 {
			detail += "\n  " + strings.Join(warnings, "\n  ")
		}
		detail += "\n  fix: run 'notion-track init' to remap, or rename the property back in Notion"
		return Check{"properties", "fail", detail}
	case len(warnings) > 0:
		return Check{"properties", "warn", strings.Join(warnings, "\n  ")}
	default:
		return Check{"properties", "ok", "all mapped properties exist with the expected types"}
	}
}

// checkAssignee verifies that the configured identity still names an option the
// column offers. An option renamed in Notion turns every "--assignee me" into a
// runtime failure, and this is the place to find that out first.
func (s *Service) checkAssignee(schema *notion.Schema) Check {
	if s.profile.Me == "" {
		return Check{"assignee", "ok", "mapped; no identity configured (--assignee me is unavailable)"}
	}
	// A missing column would otherwise reach ResolveOption as a zero-value
	// Property, and its "allowed values are:" would trail off into nothing —
	// a message that names no cause and offers no fix. checkProperties already
	// reports the missing column and what to do about it, so this says only
	// what it can no longer answer.
	prop, ok := schema.Properties[s.profile.Properties.Assignee]
	if !ok {
		return Check{"assignee", "warn", fmt.Sprintf(
			"cannot check the identity %q: the mapped column %q no longer exists",
			s.profile.Me, s.profile.Properties.Assignee)}
	}
	// "assignee", not "me": the role names the column being searched, and an
	// error reading `unknown me "Marco"` describes nothing the user can act on.
	resolved, err := tracker.ResolveOption("assignee", s.profile.Me, prop.Options)
	if err != nil {
		return Check{"assignee", "warn", fmt.Sprintf(
			"the configured identity %q no longer resolves: %v\n"+
				"  fix: rerun 'notion-track init --me <name>' with a name the column still offers",
			s.profile.Me, err)}
	}

	// The identity resolves — but where did it come from? config.yml is meant
	// to be committed and shared, so an identity that still lives there is
	// every teammate's identity: theirs resolves to whoever ran init, and their
	// "--assignee me" quietly assigns work to that person. Identities read from
	// the environment or the per-user credentials file are exactly as intended,
	// so only "legacy" is worth a warning.
	if s.profile.MeSource == "legacy" {
		return Check{"assignee", "warn", fmt.Sprintf(
			"--assignee me resolves to %s, from the config file, which is meant to be shared\n"+
				"  fix: rerun 'notion-track init --me %s' to move it to your credentials file",
			resolved, s.profile.Me)}
	}
	return Check{"assignee", "ok", "--assignee me resolves to " + resolved}
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
//
// It does not use the schema, so it still runs — and can still find real
// duplicates — even when the data_source check has failed.
//
// Cost: QueryPages pages through the whole data source, one request per 100
// rows, with no limit and no size warning. On a database with tens of
// thousands of rows this check is the expensive part of doctor.
func (s *Service) checkDuplicates(ctx context.Context) Check {
	prop := s.profile.Properties.Ticket
	if prop == "" {
		// Without a ticket property there is no key to group rows by: every
		// lookup below would read as "", making every row collide into one
		// bucket or, worse, none at all. Reporting "ok" here would hide a
		// broken mapping instead of surfacing it.
		return Check{"duplicates", "fail",
			"ticket property is not configured, cannot scan for duplicates\n" +
				"  fix: run 'notion-track init' to map it"}
	}

	pages, err := s.client.QueryPages(ctx, s.profile.DataSourceID, nil)
	if err != nil {
		return Check{"duplicates", "warn", fmt.Sprintf("could not scan rows: %v", err)}
	}

	byTicket := map[string][]notion.Page{}
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
