package notion

import (
	"context"
	"net/http"
	"net/url"
)

// PageMarkdown is the response of both GET and PATCH /v1/pages/{id}/markdown:
// the two share one shape, so appending returns the resulting page rather than
// a bare acknowledgement.
//
// Truncated and UnknownBlockIDs are carried all the way to the caller on
// purpose. Notion truncates around 20,000 blocks and renders unsupported types
// (bookmark, embed, link preview, breadcrumb, template button) as
// <unknown .../>; a reader who is not told would believe they hold the whole
// page.
type PageMarkdown struct {
	Markdown        string
	Truncated       bool
	UnknownBlockIDs []string
	// RequestID is the id Notion support asks for when diagnosing a call. It
	// costs nothing to keep and is worth quoting in an error message.
	RequestID string
}

// pageMarkdownResponse is the wire shape, kept separate so the exported type
// carries Go names rather than JSON ones.
type pageMarkdownResponse struct {
	Markdown        string   `json:"markdown"`
	Truncated       bool     `json:"truncated"`
	UnknownBlockIDs []string `json:"unknown_block_ids"`
	RequestID       string   `json:"request_id"`
}

// maxMarkdownResponseBytes caps the two /markdown responses, which are the
// only unpaginated ones this client makes: both GET and PATCH answer with the
// page's ENTIRE body in a single response, so the default 1 MiB ceiling —
// sized for paginated payloads of a few KB — is a limit on how large a page
// notion-track can handle at all, not a guard against a misbehaving proxy.
//
// On the PATCH the consequence is worse than a failed read. An oversized 200
// is discarded for size and surfaces as a non-*APIError, which
// doRejectRetryable cannot distinguish from a transport failure and so joins
// with ErrAmbiguousWrite: an append that DID land is reported as
// appended:false, permanently, on exactly the append-only pages --append-file
// exists to grow.
//
// Notion documents NO maximum size for this response (checked against both
// /reference/retrieve-page-markdown and /reference/update-page-markdown: the
// request-limits page caps payloads at 1000 blocks / 500KB, but that governs
// what you SEND, not what comes back). So this number cannot be read off the
// docs; it is derived, and deliberately generous.
//
// The one documented bound on the page itself is the record limit: Notion
// stops loading around 20,000 blocks and sets truncated=true. 20,000 blocks
// at ~500 bytes of Markdown each — well above a realistic average, since most
// blocks are a line of prose — puts a fully-rendered page near 10 MB. 32 MiB
// leaves roughly 3x headroom above that, so the cap is never what a real page
// hits first; truncated=true is.
//
// Erring high is the conservative direction HERE, which is the opposite of the
// usual instinct. This ceiling does not protect the page from anything: too
// low, and a legitimate 200 is discarded, which on the PATCH is reported as a
// failed append (see below). Its only job is to stop an unbounded read, and
// 32 MiB still does that.
const maxMarkdownResponseBytes = 32 << 20 // 32 MiB

// maxResponseBytes implements responseLimiter, so both markdown calls get the
// larger ceiling wherever they are made.
func (r *pageMarkdownResponse) maxResponseBytes() int64 { return maxMarkdownResponseBytes }

// toPageMarkdown converts the wire shape to the exported one. The two differ
// only in their JSON tags, which Go ignores when converting between otherwise
// identical struct types — so this stays a conversion rather than a
// field-by-field copy that would silently drop a field added to only one side.
func (r pageMarkdownResponse) toPageMarkdown() PageMarkdown {
	return PageMarkdown(r)
}

// GetPageMarkdown returns the page body rendered as Markdown by Notion, in one
// call: no recursion into child blocks, and block types this tool cannot build
// itself (tables, callouts, toggles) still read back correctly.
//
// GET is idempotent, so it uses do and gets the full retry policy.
func (c *Client) GetPageMarkdown(ctx context.Context, pageID string) (PageMarkdown, error) {
	var resp pageMarkdownResponse
	path := "/v1/pages/" + url.PathEscape(pageID) + "/markdown"
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return PageMarkdown{}, err
	}
	return resp.toPageMarkdown(), nil
}

// AppendPageMarkdown adds content to the END of a page, leaving everything
// already there untouched. It is the non-destructive counterpart to
// AppendBlockChildren + DeleteBlock, which together own the whole body.
//
// It returns the resulting page: the PATCH answers with the full updated
// Markdown rather than an acknowledgement, so a caller that gets a response
// knows exactly what the page now holds, with no follow-up GET. Truncated and
// UnknownBlockIDs describe the page AFTER the append, which is the moment
// worth warning about: appending is what pushes a page past the ~20,000-block
// limit, and this is the only response that can say so without a second call.
//
// PATCH here is NOT idempotent -- running it twice appends twice -- so it uses
// doRejectRetryable, which retries only the statuses where Notion certainly
// refused the request (429/503/529) and joins anything ambiguous with
// ErrAmbiguousWrite instead of guessing.
//
// content must be non-empty: Notion answers 200 and does nothing at all for an
// empty string (verified, spec §10.e), so an empty append would look like a
// success. Callers validate before reaching here.
func (c *Client) AppendPageMarkdown(ctx context.Context, pageID, content string) (PageMarkdown, error) {
	body := map[string]any{
		"type": "insert_content",
		"insert_content": map[string]any{
			"content": content,
			// Sent explicitly: omitting it appends too, but that default is not
			// in the reference and is not worth depending on.
			"position": map[string]string{"type": "end"},
		},
	}
	var resp pageMarkdownResponse
	path := "/v1/pages/" + url.PathEscape(pageID) + "/markdown"
	if err := c.doRejectRetryable(ctx, http.MethodPatch, path, body, &resp); err != nil {
		return PageMarkdown{}, err
	}
	return resp.toPageMarkdown(), nil
}
