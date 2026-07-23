# Page id format verification

Consulted: 2026-07-23, via WebFetch against `developers.notion.com`, for the page-id
addressing feature (`get --page-id` / `set --page-id`).

## Question

A user pastes a page id copied from Notion in one of three shapes: the full page URL,
a bare 32-character hex string, or a dashed UUID. Which form does the API actually
require for `page_id` path parameters (`GET /v1/pages/{page_id}`,
`PATCH /v1/pages/{page_id}`)?

## Finding

- <https://developers.notion.com/reference/retrieve-a-page> documents `page_id` only
  as `idRequest` / `type: string`, with no format constraint spelled out on that page.
- <https://developers.notion.com/docs/working-with-page-content> is explicit about the
  canonical form when extracting an id from a URL: **"The URL ends in a page ID. It
  should be a 32 character long string. Format this value by inserting hyphens (-) in
  the following pattern: 8-4-4-4-12."** The guide's own example transforms
  `1429989fe8ac4effbc8f57f56486db54` into `1429989f-e8ac-4eff-bc8f-57f56486db54`, and
  every code sample on that page uses the dashed form.

## Conclusion

`internal/notion.NormalizePageID` canonicalises all three accepted input shapes (full
URL, bare 32-hex, dashed UUID) to the dashed 8-4-4-4-12 UUID form — the one the docs
describe as correct and the one every Notion API response returns `id` fields in. This
sidesteps the open question of whether the API would also *accept* an undashed 32-hex
id on write paths: notion-track only ever sends the documented, unambiguous form.

## Residual uncertainty

The `retrieve-a-page` reference itself does not state whether an undashed 32-hex id
would be *rejected*. Not acted on: since notion-track always sends the dashed form, it
never depends on the answer either way.
