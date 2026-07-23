package notion

import (
	"context"
	"net/http"
	"net/url"
)

// GetSchema reads the property schema of a data source.
//
// Before API 2025-09-03 this lived on GET /v1/databases/{id}; that endpoint now
// returns the list of data sources instead, and the schema moved here.
func (c *Client) GetSchema(ctx context.Context, dataSourceID string) (*Schema, error) {
	type option struct {
		Name string `json:"name"`
	}
	type rawProperty struct {
		Type   string `json:"type"`
		Select *struct {
			Options []option `json:"options"`
		} `json:"select"`
		Status *struct {
			Options []option `json:"options"`
		} `json:"status"`
	}
	var resp struct {
		ID         string                 `json:"id"`
		Title      []RichText             `json:"title"`
		Properties map[string]rawProperty `json:"properties"`
	}

	// PathEscape, not raw concatenation: the id reaches here from a config file
	// or a command-line flag, and an unescaped "/" or "?" would silently
	// retarget the request instead of failing.
	path := "/v1/data_sources/" + url.PathEscape(dataSourceID)
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}

	schema := &Schema{
		DataSourceID: resp.ID,
		Title:        PlainText(resp.Title),
		Properties:   make(map[string]Property, len(resp.Properties)),
	}
	for name, raw := range resp.Properties {
		p := Property{Name: name, Type: raw.Type}
		switch {
		case raw.Select != nil:
			for _, o := range raw.Select.Options {
				p.Options = append(p.Options, o.Name)
			}
		case raw.Status != nil:
			for _, o := range raw.Status.Options {
				p.Options = append(p.Options, o.Name)
			}
		}
		schema.Properties[name] = p
	}
	return schema, nil
}
