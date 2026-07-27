package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// EqualsFilter builds an equality filter for one property. The filter body is
// keyed by property type, so the caller must pass the type from the schema.
//
// Only the types notion-track matches on are meaningful here: title,
// rich_text, select and status. Other types either need a different operator
// (multi_select and relation want "contains") or a non-string value
// (checkbox wants a bool), and Notion rejects the resulting filter with a
// validation error rather than returning wrong rows — loud, but still the
// caller's mistake to avoid.
func EqualsFilter(property, propType, value string) Filter {
	return Filter{
		"property": property,
		propType:   map[string]string{"equals": value},
	}
}

// IsEmptyFilter matches rows where a property carries no value. Like
// EqualsFilter, the body is keyed by property type, so the caller passes the
// type from the schema.
func IsEmptyFilter(property, propType string) Filter {
	return Filter{
		"property": property,
		propType:   map[string]bool{"is_empty": true},
	}
}

// AndFilter combines filters into a compound one, skipping the nil entries a
// caller building a filter from optional flags naturally produces.
//
// A lone filter is returned unwrapped, and no filters at all yield nil (which
// QueryPages reads as "every row"): both keep the request identical to what
// callers sent before compounding existed.
func AndFilter(filters ...Filter) Filter {
	var present []Filter
	for _, f := range filters {
		if f != nil {
			present = append(present, f)
		}
	}
	switch len(present) {
	case 0:
		return nil
	case 1:
		return present[0]
	default:
		return Filter{"and": present}
	}
}

// QueryPages returns every row matching filter, following pagination.
// A nil filter returns all rows.
func (c *Client) QueryPages(ctx context.Context, dataSourceID string, filter Filter) ([]Page, error) {
	type queryReq struct {
		Filter      Filter `json:"filter,omitempty"`
		StartCursor string `json:"start_cursor,omitempty"`
		PageSize    int    `json:"page_size,omitempty"`
	}
	var out []Page
	cursor := ""
	for {
		var resp struct {
			Results    []json.RawMessage `json:"results"`
			HasMore    bool              `json:"has_more"`
			NextCursor string            `json:"next_cursor"`
		}
		req := queryReq{Filter: filter, StartCursor: cursor, PageSize: 100}
		// url.PathEscape: the id comes from config or a flag, and an unescaped
		// "/" would silently retarget the request.
		path := "/v1/data_sources/" + url.PathEscape(dataSourceID) + "/query"
		if err := c.do(ctx, http.MethodPost, path, req, &resp); err != nil {
			return nil, err
		}
		for _, raw := range resp.Results {
			p, err := decodePage(raw)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
		if !resp.HasMore || resp.NextCursor == "" {
			return out, nil
		}
		// Same guard as ListDataSources: a cursor that does not advance would
		// loop forever, appending the same page every time.
		if resp.NextCursor == cursor {
			return nil, fmt.Errorf(
				"notion: query pagination stalled, cursor %q repeated", resp.NextCursor)
		}
		cursor = resp.NextCursor
	}
}

// decodePage flattens Notion's per-type property shapes into PropertyValue.
func decodePage(raw json.RawMessage) (Page, error) {
	var envelope struct {
		ID             string    `json:"id"`
		URL            string    `json:"url"`
		LastEditedTime time.Time `json:"last_edited_time"`
		Parent         struct {
			DataSourceID string `json:"data_source_id"`
		} `json:"parent"`
		Properties map[string]struct {
			Type     string     `json:"type"`
			Title    []RichText `json:"title"`
			RichText []RichText `json:"rich_text"`
			Select   *struct {
				Name string `json:"name"`
			} `json:"select"`
			Status *struct {
				Name string `json:"name"`
			} `json:"status"`
			Date *struct {
				Start string `json:"start"`
			} `json:"date"`
			Checkbox bool `json:"checkbox"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Page{}, err
	}

	p := Page{
		ID:             envelope.ID,
		URL:            envelope.URL,
		LastEditedTime: envelope.LastEditedTime,
		DataSourceID:   envelope.Parent.DataSourceID,
		Properties:     make(map[string]PropertyValue, len(envelope.Properties)),
	}
	for name, v := range envelope.Properties {
		pv := PropertyValue{Type: v.Type, Checkbox: v.Checkbox}
		switch v.Type {
		case "title":
			pv.Text = PlainText(v.Title)
		case "rich_text":
			pv.Text = PlainText(v.RichText)
		case "select":
			if v.Select != nil {
				pv.Text = v.Select.Name
			}
		case "status":
			if v.Status != nil {
				pv.Text = v.Status.Name
			}
		case "date":
			if v.Date != nil {
				pv.Date = v.Date.Start
			}
		}
		p.Properties[name] = pv
	}
	return p, nil
}
