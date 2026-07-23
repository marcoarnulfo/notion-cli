package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// CreatePage adds a row to a data source. props holds already-built Notion
// property values, keyed by property name; building them is tracker's job.
//
// Unlike every other request this client makes, this one is not idempotent:
// each successful call creates a new row. It therefore goes through
// doNonRetryable rather than do — see that method for why retrying it would
// risk creating a duplicate row instead of recovering from a transient error.
func (c *Client) CreatePage(ctx context.Context, dataSourceID string, props map[string]any) (Page, error) {
	body := map[string]any{
		"parent": map[string]string{
			"type":           "data_source_id",
			"data_source_id": dataSourceID,
		},
		"properties": props,
	}
	var raw json.RawMessage
	if err := c.doNonRetryable(ctx, http.MethodPost, "/v1/pages", body, &raw); err != nil {
		return Page{}, err
	}
	return decodePage(raw)
}

// UpdatePage patches properties on an existing row.
func (c *Client) UpdatePage(ctx context.Context, pageID string, props map[string]any) (Page, error) {
	var raw json.RawMessage
	body := map[string]any{"properties": props}
	// PathEscape for the same reason as elsewhere in this package: the id comes
	// from a query result or user input, and an unescaped separator would
	// retarget the request instead of failing.
	path := "/v1/pages/" + url.PathEscape(pageID)
	if err := c.do(ctx, http.MethodPatch, path, body, &raw); err != nil {
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
