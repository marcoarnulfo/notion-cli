# Notion API version verification

Consulted: 2026-07-23. All facts below were checked against `developers.notion.com`
today via WebFetch/WebSearch, not recalled from training data (training cutoff predates
this date, and the 2025-09-03 and 2026-03-11 breaking changes postdate it).

## 1. Version chosen

```
APIVersion = "2026-03-11"
```

The project floor (design doc §2) is "never below `2025-09-03`". `2026-03-11` is the
next stable version after that floor, and — as of the consultation date — the most
recent one:

- Changelog — <https://developers.notion.com/page/changelog> (consulted 2026-07-23):
  lists exactly two API-version entries after the pre-2025 baseline:
  - `2025-09-03` — introduces multi-source databases (databases become containers,
    data sources become the queryable/writable unit).
  - `2026-03-11` — breaking changes: `position` replaces `after` for block
    placement, `in_trash` replaces `archived` on read paths, `transcription` block
    type renamed to `meeting_notes`. No entry newer than `2026-03-11` is listed.
- Versioning reference — <https://developers.notion.com/reference/versioning>
  (consulted 2026-07-23): states explicitly **"The current API version is
  2026-03-11."** This is a second, independent confirmation that `2026-03-11` is
  the current stable version, not just the latest changelog entry.

Both sources agree, so `2026-03-11` is used rather than `2025-09-03`: it is
strictly newer, still ≥ the mandatory floor, and is what the official docs call
"current" today.

## 2. The four claims to confirm or deny (per task context, point 3)

### a. Query a data source is `POST`, not `PATCH`

**CONFIRMED.** `POST /v1/data_sources/{data_source_id}/query`.
Source: <https://developers.notion.com/reference/query-a-data-source> (2026-07-23).
Request body: `filter`, `sorts` (`property`/`timestamp` + `direction`),
`start_cursor`, `page_size`, `is_archived`. Response: `object: "list"`,
`results: Array<Page | DataSource>`, `next_cursor`, `has_more`,
`request_status.type` (`"complete" | "incomplete"`, with
`incomplete_reason: "query_result_limit_reached"` when the 10,000-result query cap
is hit).

### b. Search filter value for data sources is `{"property": "object", "value": "data_source"}`

**CONFIRMED.** Source: <https://developers.notion.com/reference/post-search>
(2026-07-23). `filter.property` is always the literal string `"object"`;
`filter.value` accepts `"page"` or `"data_source"`. `filter.in_trash` is a separate,
optional boolean. Full request body:

```json
{
  "query": "string (optional)",
  "sort": { "timestamp": "last_edited_time", "direction": "ascending|descending" },
  "start_cursor": "uuid (optional)",
  "page_size": 100,
  "filter": { "property": "object", "value": "page|data_source", "in_trash": false }
}
```

Response: `object: "list"`, `results` (array of page/data-source objects),
`next_cursor`, `has_more`, `request_status`.

### c. Create-page parent uses `{"type": "data_source_id", "data_source_id": "..."}`

**CONFIRMED.** Source: <https://developers.notion.com/reference/post-page>
(2026-07-23). `parent` accepts one of four shapes: `page_id`, `database_id`
(legacy), `data_source_id` (current, required for data-source-backed rows), or
`workspace: true`. Example:

```json
{
  "parent": { "type": "data_source_id", "data_source_id": "d9824bdc-8445-4327-be8b-5b47500af6ce" },
  "properties": { "Name": { "title": [{ "text": { "content": "New Page Title" } }] } }
}
```

### d. `GET /v1/data_sources/{id}` returns `title` as rich-text array and `properties` as name→schema map, with options inside `select`/`status`

**CONFIRMED.** Source: <https://developers.notion.com/reference/retrieve-a-data-source>
(2026-07-23). `GET /v1/data_sources/{data_source_id}`. Response includes `title`
(array of rich text objects, subject to the general 100-element array cap) and
`properties` (map keyed by the human-readable property name, values are property
schema objects). Option layout differs between the two types:

```json
"select": { "options": [ { "id": "...", "name": "...", "color": "...", "description": "..." } ] }
"status": {
  "options": [ { "id": "...", "name": "...", "color": "...", "description": "..." } ],
  "groups":  [ { "id": "...", "name": "...", "color": "...", "option_ids": ["..."] } ]
}
```

`status` additionally carries `groups` (the visual "To-do / In progress / Complete"
buckets); `select` does not.

All four claims from prior verification hold — none needed to be corrected.

## 3. Other endpoints the client will need (tasks 3-8), method + path

Verified alongside the four above so implementers don't have to go back to the web.

| Purpose | Method & path | Notes |
|---|---|---|
| List a database's data sources (discovery step 1) | `GET /v1/databases/{database_id}` | Response has a `data_sources` array of `{id, name}` (max 100 items). It does **not** return `properties` directly any more — that moved to `GET /v1/data_sources/{id}` (step 2). Source: <https://developers.notion.com/reference/retrieve-a-database> (2026-07-23). |
| Get data source schema (discovery step 2) | `GET /v1/data_sources/{data_source_id}` | See §2.d above. |
| List/search shared data sources | `POST /v1/search` | See §2.b above. |
| Query rows of a data source | `POST /v1/data_sources/{data_source_id}/query` | See §2.a above. |
| Create a row (page) | `POST /v1/pages` | `parent.type = "data_source_id"`. See §2.c above. |
| Update a row's properties (`set`) | `PATCH /v1/pages/{page_id}` | Body: `{"properties": {"PropName": {"type": "...", "...": ...}}}`. `in_trash` (move to trash) and `is_archived` (archive) are now separate boolean flags — do not conflate them. Source: <https://developers.notion.com/reference/patch-page> (2026-07-23). |
| Replace a page's body (Markdown → blocks) | `PATCH /v1/blocks/{block_id}/children` | Max 100 block children per request; max 2 levels of nesting per request. `position` (introduced 2026-03-11) replaces the deprecated `after` field — accepts `{"type": "end"}`, `{"type": "start"}`, or `{"type": "after_block", "after_block": {"id": "..."}}`. Source: <https://developers.notion.com/reference/patch-block-children> (2026-07-23). |
| Verify token / `doctor` check 1 | `GET /v1/users/me` | Returns bot user: `id`, `object: "user"`, `type: "bot"`, `bot.{owner, workspace_name, workspace_limits}`. Source: <https://developers.notion.com/reference/get-self> (2026-07-23). |

## 4. Numeric limits (for `internal/notion` retry logic and `internal/markdown` chunking)

Verbatim from <https://developers.notion.com/reference/request-limits> (consulted
2026-07-23):

- **Rate limit** — "Per connection — an average of three requests per second, with
  some bursts beyond the average allowed." (One shared bucket across all callers of
  the same integration token — CI jobs, TUI, and every teammate.)
- **Payload size** — "payloads have a maximum size of 1000 block elements and 500KB
  overall." (This is the outer request-body cap, distinct from the 100-block-per-call
  limit on the append-children endpoint specifically — see the table row above.)
- **Array of blocks or rich text objects** — 100 elements per array.
- **Rich text `text.content`** — 2000 characters.
- **Rich text `text.link.url`** — 2000 characters.
- **Rich text `equation.expression`** — 1000 characters.
- **Any URL property** — 2000 characters.
- **Any email property** — 200 characters.
- **Any phone number property** — 200 characters.
- **Multi-select** — 100 options per request.
- **Relation** — 100 related pages per request.
- **People property** — 100 users per request.
- **429 handling** — Notion returns `429` with a `Retry-After` header (seconds) when
  the rate limit is exceeded; the design doc's retry-with-backoff requirement (§9)
  must honor this header rather than using a fixed backoff.
- **529 handling** — the same page states that connections "should accommodate
  variable rate limits by handling HTTP 429 **and 529** responses and respecting the
  `Retry-After` response header value". <https://developers.notion.com/reference/errors>
  documents `529` as `service_overload`: "Notion is temporarily overloaded. Respect
  the Retry-After response header and try again later."
  **`529` must be retried exactly like `429`**, `Retry-After` included. The design
  doc §9 lists only `429`/`502`/`503`/`504` and is therefore incomplete on this
  point; `internal/notion` follows these notes.

Reconciling with the design doc's chunking numbers (§10: "massimo 100 blocchi per
chiamata di append, massimo 1000 blocchi per payload, massimo 2000 caratteri per
`rich_text`"): confirmed on all three counts — 100 is the per-append-call cap (block
children endpoint), 1000 is the overall per-payload block cap, 2000 is the rich-text
`content`/`url` character cap.

## 5. Residual uncertainties

- Content was retrieved through the WebFetch tool, which renders pages through an
  intermediate summarization model rather than returning raw HTML. Where a claim was
  load-bearing (the four in §2, the exact limits in §4), I re-fetched with an
  explicit "quote verbatim" instruction to reduce paraphrasing risk, and cross-checked
  the version number against two independent pages (changelog + versioning
  reference). I could not diff against the raw OpenAPI spec/HTML source, so a small
  residual risk of the fetch tool's summarization dropping or softening a qualifier
  remains — if task 3+ implementation surfaces a response shape that contradicts this
  note, trust the live API response over this document and update this file.
- Workspace-level (as opposed to per-connection) rate-limit tiers are mentioned as
  "scale based on plan" but the exact numbers weren't surfaced. Not acted on: the
  client's retry-with-backoff-on-429 design is reactive and tier-agnostic by
  construction (design doc §9), so this doesn't block implementation.
- `GET /v1/databases/{id}` docs contain a note that reads as if the endpoint itself
  were "deprecated as of 2025-09-03." Read in context, this describes the removal of
  the direct `properties` field from that endpoint's response (properties moved to
  the data source), not deprecation of the endpoint. **Correction after review**: the
  "Deprecated as of version 2025-09-03" line marks the *versioned documentation page*
  for API `2022-06-28` and earlier ("Refer to the new APIs instead"), not the removal
  of a single field. The practical conclusion is unchanged and still correct — the
  endpoint is live, its response now carries `data_sources[]` instead of `properties`,
  and it remains required by the two-step discovery flow in design doc §2/§5.

## 6. Property value write shapes (added after Task 12 review)

The sections above documented `title` only, via the create-page example. Verified
against <https://developers.notion.com/reference/page-property-values> (consulted
2026-07-23), these are the write shapes `internal/tracker` builds:

| Type | Payload |
|---|---|
| `title` | `{"title": [{"text": {"content": "..."}}]}` |
| `rich_text` | `{"rich_text": [{"text": {"content": "..."}}]}` |
| `select` | `{"select": {"name": "..."}}` |
| `status` | `{"status": {"name": "..."}}` |
| `date` | `{"date": {"start": "YYYY-MM-DD"}}` |

The documented examples also carry `"type": "text"` and a full `annotations`
object on rich text fragments. Both are optional on write — Notion fills in the
defaults — so notion-track sends the minimal form.
