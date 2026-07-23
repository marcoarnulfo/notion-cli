package tracker

import (
	"fmt"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

// Fields are the values a user asked to write. Empty strings mean "leave this
// property alone", which is what makes `set --status` a partial update.
type Fields struct {
	Ticket string
	Title  string
	Status string
	Due    string
}

// BuildProperties turns user fields into a Notion properties payload, using
// the configured mapping and the live schema to pick each property's shape.
//
// The schema is the authority on types: a status column and a select column
// take different payloads, and which one a database uses is not our choice.
func BuildProperties(f Fields, props config.Properties, schema *notion.Schema) (map[string]any, error) {
	out := map[string]any{}

	add := func(propName, value string) error {
		if value == "" || propName == "" {
			return nil
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
			if err := ValidateStatus(value, prop.Options); err != nil {
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

	// Title first: when the ticket key *is* the title column, the ticket value
	// must win over a separately supplied title.
	if err := add(props.Title, f.Title); err != nil {
		return nil, err
	}
	if err := add(props.Ticket, f.Ticket); err != nil {
		return nil, err
	}
	if err := add(props.Status, f.Status); err != nil {
		return nil, err
	}
	if err := add(props.Due, f.Due); err != nil {
		return nil, err
	}
	return out, nil
}
