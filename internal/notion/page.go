package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// CreatePage adds a row to a data source. props holds already-built Notion
// property values, keyed by property name; building them is tracker's job.
func (c *Client) CreatePage(ctx context.Context, dataSourceID string, props map[string]any) (Page, error) {
	body := map[string]any{
		"parent": map[string]string{
			"type":           "data_source_id",
			"data_source_id": dataSourceID,
		},
		"properties": props,
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, "/v1/pages", body, &raw); err != nil {
		return Page{}, err
	}
	return decodePage(raw)
}

// UpdatePage patches properties on an existing row.
func (c *Client) UpdatePage(ctx context.Context, pageID string, props map[string]any) (Page, error) {
	var raw json.RawMessage
	body := map[string]any{"properties": props}
	if err := c.do(ctx, http.MethodPatch, "/v1/pages/"+url.PathEscape(pageID), body, &raw); err != nil {
		return Page{}, err
	}
	return decodePage(raw)
}

// Me returns the name of the bot the token belongs to. doctor uses it as the
// cheapest possible proof that the token is valid.
func (c *Client) Me(ctx context.Context) (string, error) {
	var resp struct {
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/users/me", nil, &resp); err != nil {
		return "", err
	}
	return resp.Name, nil
}
