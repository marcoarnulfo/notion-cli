package notion

import (
	"context"
	"net/http"
)

// ListDataSources returns every data source shared with this integration.
//
// Notion's search index is eventually consistent, but objects shared directly
// with a connection are guaranteed to appear — which is exactly our case.
// Callers should still offer a retry: an owner may have shared the database
// seconds ago.
func (c *Client) ListDataSources(ctx context.Context) ([]DataSourceRef, error) {
	type searchReq struct {
		Filter      map[string]string `json:"filter"`
		StartCursor string            `json:"start_cursor,omitempty"`
		PageSize    int               `json:"page_size,omitempty"`
	}
	type searchResp struct {
		Results []struct {
			ID     string     `json:"id"`
			Title  []RichText `json:"title"`
			Parent struct {
				DatabaseID string `json:"database_id"`
			} `json:"parent"`
		} `json:"results"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}

	var out []DataSourceRef
	cursor := ""
	for {
		req := searchReq{
			Filter:      map[string]string{"property": "object", "value": "data_source"},
			StartCursor: cursor,
			PageSize:    100,
		}
		var resp searchResp
		if err := c.do(ctx, http.MethodPost, "/v1/search", req, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Results {
			out = append(out, DataSourceRef{
				ID:         r.ID,
				Title:      PlainText(r.Title),
				DatabaseID: r.Parent.DatabaseID,
			})
		}
		if !resp.HasMore || resp.NextCursor == "" {
			return out, nil
		}
		cursor = resp.NextCursor
	}
}
