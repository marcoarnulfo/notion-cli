package notion

// APIVersion is the Notion-Version header sent on every request.
//
// Notion 2025-09-03 introduced data sources as a breaking change: databases
// can hold several of them, and schema, query and page-parent all moved to
// data-source endpoints. Staying on an older version breaks reads, writes and
// queries as soon as anyone adds a second data source from the UI, so this
// floor is not negotiable.
//
// 2026-03-11 is the current stable version at the time of verification (per
// the official versioning reference page, which states this explicitly), and
// it stays above the 2025-09-03 floor. It additionally replaces the
// deprecated `after` field on block-children append with `position`, and
// `archived` with `in_trash` on read paths.
//
// Value verified against the official changelog and versioning reference;
// see docs/superpowers/notes/2026-07-23-notion-api-version.md
const APIVersion = "2026-03-11"

// BaseURL is the Notion API root. Tests override it with an httptest server.
const BaseURL = "https://api.notion.com"
