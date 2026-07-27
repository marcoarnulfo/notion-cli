package tracker

import (
	"errors"
	"fmt"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

// Fields are the values a user asked to write. Empty strings mean "leave this
// property alone", which is what makes `set --status` a partial update.
//
// The field order is load-bearing: internal/cli converts mcp.Fields to this
// type directly, which only compiles while the two stay identical.
type Fields struct {
	Ticket   string
	Title    string
	Status   string
	Due      string
	Assignee string
	// Unassign clears the assignee column. It is a separate field rather than a
	// reserved Assignee value ("none", "") because every empty value in this
	// struct already means "leave this alone", and one exception to that rule
	// would be one too many.
	Unassign bool
}

// ErrConflictingAssignee marks a request that both sets and clears the
// assignee. The CLI's flags already exclude each other, but apply and the MCP
// server never touch cobra: the rule has to live where every caller passes.
var ErrConflictingAssignee = errors.New("cannot set and clear the assignee in the same write")

// BuildProperties turns user fields into a Notion properties payload, using
// the configured mapping and the live schema to pick each property's shape.
//
// The schema is the authority on types: a status column and a select column
// take different payloads, and which one a database uses is not our choice.
func BuildProperties(f Fields, props config.Properties, schema *notion.Schema) (map[string]any, error) {
	out := map[string]any{}

	// role names the init flag in the "not mapped" error below (e.g. "due" ->
	// --due-prop) and, for select properties, is also the field name that
	// ValidateOption attaches to ValidationError.
	add := func(role, propName, value string) error {
		if value == "" {
			// Empty means "leave this property alone" regardless of whether
			// it is mapped — this is what makes `set --status` a partial
			// update, and it must hold even for a role nobody configured.
			return nil
		}
		if propName == "" {
			// The user passed a value explicitly; silently dropping it would
			// lose data. init does not require every role (due is optional),
			// so this is the normal shape of "not configured yet", not a
			// corrupted config — the fix is to map it, not to fail loudly
			// about a broken mapping.
			return fmt.Errorf(
				"%s was set to %q but no %s property is mapped; "+
					"run 'notion-track init --%s-prop <name>' to map it",
				role, value, role, role)
		}
		prop, ok := schema.Properties[propName]
		if !ok {
			return fmt.Errorf(
				"property %q is configured but does not exist in the data source; "+
					"run 'notion-track doctor' to see the current schema", propName)
		}
		switch prop.Type {
		case "title":
			out[propName] = map[string]any{
				"title": []map[string]any{{"text": map[string]string{"content": value}}},
			}
		case "rich_text":
			out[propName] = map[string]any{
				"rich_text": []map[string]any{{"text": map[string]string{"content": value}}},
			}
		case "status":
			if err := ValidateStatus(value, prop.Options); err != nil {
				return err
			}
			out[propName] = map[string]any{"status": map[string]string{"name": value}}
		case "select":
			if err := ValidateOption(role, value, prop.Options); err != nil {
				return err
			}
			out[propName] = map[string]any{"select": map[string]string{"name": value}}
		case "date":
			out[propName] = map[string]any{"date": map[string]string{"start": value}}
		default:
			return fmt.Errorf("property %q has unsupported type %q", propName, prop.Type)
		}
		return nil
	}

	if f.Assignee != "" && f.Unassign {
		return nil, ErrConflictingAssignee
	}

	// Title first: when the ticket key *is* the title column, the ticket value
	// must win over a separately supplied title.
	if err := add("title", props.Title, f.Title); err != nil {
		return nil, err
	}
	if err := add("ticket", props.Ticket, f.Ticket); err != nil {
		return nil, err
	}
	if err := add("status", props.Status, f.Status); err != nil {
		return nil, err
	}
	if err := add("due", props.Due, f.Due); err != nil {
		return nil, err
	}
	if err := add("assignee", props.Assignee, f.Assignee); err != nil {
		return nil, err
	}
	// Clearing is the one write that has to happen for an empty value, so it
	// cannot go through add, which is built around the opposite rule.
	if f.Unassign {
		if props.Assignee == "" {
			return nil, fmt.Errorf(
				"unassign was requested but no assignee property is mapped; " +
					"run 'notion-track init --assignee-prop <name>' to map it")
		}
		if _, ok := schema.Properties[props.Assignee]; !ok {
			return nil, fmt.Errorf(
				"property %q is configured but does not exist in the data source; "+
					"run 'notion-track doctor' to see the current schema", props.Assignee)
		}
		out[props.Assignee] = map[string]any{"select": nil}
	}
	return out, nil
}
