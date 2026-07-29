**English** · [Italiano](README.it.md)

# notion-track

[![CI](https://github.com/marcoarnulfo/notion-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/marcoarnulfo/notion-cli/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/marcoarnulfo/notion-cli)](https://github.com/marcoarnulfo/notion-cli/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/marcoarnulfo/notion-cli)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

> A small, opinionated Go CLI to keep a Notion task-tracking database in sync from your terminal — and from CI. Free and open-source (MIT).

`notion-track` knows about one thing: a Notion database where each row is a ticket, identified by a ticket key, with a status and a handful of other properties. Its core operation is an **idempotent upsert** — given a ticket key, find the row and update it, or create it if it doesn't exist. Run it twice, get one row.

It authenticates with a Notion **internal integration token** only — no browser OAuth. That token is a bot with its own, narrowly-shared permissions, which is what makes it work identically for workspace members and workspace guests alike, and what keeps it usable behind firewalls that block Notion's hosted MCP endpoint.

## Features

- **Idempotent upsert** (`upsert`) — create-or-update a ticket row by ticket key. Two runs, one row.
- **Update-only write** (`set`) — fails with a distinct exit code if the ticket doesn't exist yet, instead of silently creating it.
- **Read** (`get`, `list`) — one row or many, optionally filtered by status, assignee or priority, human-readable or `--json`.
- **Interactive browsing** (`notion-track` with no arguments, at a terminal) — a TUI over the tracked rows: filter by status, change a status inline, open a row in Notion, create one without leaving the view.
- **Diagnostics** (`doctor`) — checks the token, data source access, the property mapping (including type drift since `init`), scans the whole data source for duplicate ticket keys, and warns if a git-tracked file looks like it carries your integration token.
- **Guided setup** (`init`) — a bare `notion-track init` at a terminal opens a wizard that picks the data source and proposes the property mapping for you; the flag form writes the same profile non-interactively, validated against the data source's live schema before anything is saved. `init --list` discovers the data source ids your integration can see. At an interactive terminal it also offers to collect and save the integration token if none is found (see [Configuration](#configuration)).
- **Profiles** — several named database configurations in one YAML config file, selectable by flag, environment variable, or a configured default.
- **Bulk writes** (`apply`) — many upserts and sets from one JSON or CSV manifest, applied in order, stopping at the first failure.
- **Dry run** (`--dry-run` on `upsert`/`set`) — reports whether it would create or update, which row and which columns, and writes nothing.
- **`--json` everywhere** — every command that produces output (`get`, `list`, `doctor`, `upsert`, `set`) can emit machine-readable JSON with a documented, stable shape.
- **CI-friendly by design** — quiet on success, a distinct exit code per failure class (auth, not found, duplicate, usage, generic), no interactive prompts.
- **Retries with backoff** on Notion's rate limiting (429) and transient 502/503/504/529 responses, honoring `Retry-After` when Notion sends one.
- **A single static Go binary** — no Node runtime, no Python venv.

## Requirements

- **[Go](https://go.dev/dl/) 1.26 or newer** — needed to build or install from source. Not needed once a release is published: tagged releases ship prebuilt binaries (see [Installation](#installation)).
- A **Notion internal integration token** (`ntn_...`), created by a **Workspace Owner** at <https://www.notion.so/my-integrations>.
- A Notion database **shared with that integration**.

## Installation

```bash
go install github.com/marcoarnulfo/notion-cli/cmd/notion-track@latest
```

This installs the `notion-track` binary into `$(go env GOPATH)/bin` (make sure it's on your `PATH`).

<details>
<summary>Build from source</summary>

```bash
git clone https://github.com/marcoarnulfo/notion-cli.git
cd notion-cli
go build -o notion-track ./cmd/notion-track
./notion-track --help
```
</details>

<details>
<summary>Prebuilt binaries</summary>

Every tagged release publishes static binaries for macOS, Linux and Windows (amd64 and arm64) on the [releases page](https://github.com/marcoarnulfo/notion-cli/releases), with a `checksums.txt` to verify them against:

```bash
tag=v0.6.0            # pick the release you want
os=linux arch=amd64   # or darwin/arm64

gh release download "$tag" --repo marcoarnulfo/notion-cli \
  --pattern "notion-track_${tag#v}_${os}_${arch}.tar.gz" --pattern checksums.txt
sha256sum --check --ignore-missing checksums.txt   # or: shasum -a 256 --check --ignore-missing checksums.txt
tar -xzf "notion-track_${tag#v}_${os}_${arch}.tar.gz" notion-track
```

Windows ships as a `.zip` of the same name — swap the pattern and unzip it instead.

The binaries carry no cgo, so they run on any image with or without a libc. `notion-track --version` reports the release tag; a build from source reports `dev`.

Note: until the first tag is pushed the releases page is empty, and `go install` above is the only route.
</details>

## Quick start

1. **Create the integration.** A Workspace Owner goes to <https://www.notion.so/my-integrations>, creates a new **internal** integration, and copies its token (`ntn_...`). Only a Workspace Owner can do this step.
2. **Share the database with it.** Still as a Workspace Owner, open the tracking database in Notion → **•••** (top right) → **Connections** → add the integration. Without this step every request `notion-track` makes will 404, token or no token.
3. **Give `notion-track` the token.** Either export it yourself:
   ```bash
   export NOTION_TOKEN=ntn_...
   ```
   or skip this step and let `init` (step 5) ask for it interactively — at a real terminal it prompts without echoing the token, and offers to save it to `credentials.yml` so you don't have to export it again next session. See [Configuration](#configuration) for where that file lives and how it differs from `config.yml`.
4. **Find the data source id.** A database can hold more than one data source, so ask the integration what it can see:
   ```bash
   notion-track init --list
   ```
5. **Configure a profile**, mapping notion-track's concepts onto your database's real property names — `init` validates every property against the live schema before writing anything:
   ```bash
   notion-track init \
     --data-source-id <id> \
     --ticket-prop Ticket \
     --status-prop Status \
     --title-prop Name
   ```
6. **Sanity-check the setup:**
   ```bash
   notion-track doctor
   ```
7. **Create or update a row** — this is the command you'll actually run day to day:
   ```bash
   notion-track upsert --ticket BDF-231 --title "Hardening" --status "In progress"
   notion-track upsert --ticket BDF-231 --status "Done"   # updates the same row, no duplicate
   ```
8. **(Optional) Track who owns each row.** Map a `select` column with `--assignee-prop` (see [Usage](#usage) below), export your own identity once, and `me` works everywhere `--assignee` is accepted:
   ```bash
   # once, in your shell profile
   export NOTION_TRACK_ME="Marco Arnulfo"

   notion-track set --ticket BDF-231 --status "In progress" --assignee me
   notion-track list --assignee me --status "To do"
   notion-track list --unassigned
   ```
9. **(Optional) Track how urgent each row is.** Map a `select` column with `--priority-prop` (see [Usage](#usage) below); there's no identity to export for this one, so it's ready to use as soon as it's mapped:
   ```bash
   notion-track list --priority ALTA --status "To do"
   notion-track list --priority ALTA --assignee me
   notion-track set --ticket BDF-1 --priority alta --assignee mirko
   ```

## Usage

Global flags, available on every command:

| Flag | Meaning |
|---|---|
| `--profile string` | config profile to use (see [Configuration](#configuration)) |
| `--config string` | path to an explicit config file, instead of the default OS location |

### `notion-track` — the browsing TUI

```
notion-track
```

With no arguments at a terminal, `notion-track` opens an interactive view over the tracked rows: one line each, showing the ticket key, the title, the status and the due date. `enter` opens a full-screen detail; `s` moves the selected row to another status, picked from the values the schema actually accepts; `f` narrows the list to one status; `n` creates a row without leaving the view; `o` opens it in Notion, `y` copies its URL, `r` reloads, `/` filters by text, `q` quits.

It is a view over the same `internal/service` layer every command uses — no separate logic, and nothing it can do that the flags cannot.

Creating a row while a status filter is active gives it that status, so the new row lands in the view you are looking at. A write that fails leaves the list on screen and reports the reason in one line, rather than tearing the UI down: the rows are still readable and still correct.

Without a terminal — piped, redirected, in CI — `notion-track` with no arguments prints help and exits, exactly as before.

### `init` — configure a profile

Two forms. At a terminal, with nothing else on the command line:

```
notion-track init
```

opens a wizard: it picks up your token (asking for it only if there isn't one yet), lists the data sources shared with your integration, and lets you choose one with the arrow keys. It then proposes a property mapping, guessed from your column names and types, for you to confirm or change — each role offering only columns it can actually use, so a mapping that would break on first use cannot be chosen. `enter` saves; `esc` or `Ctrl-C` cancels, writing nothing and exiting non-zero so a script can tell the two apart. `--profile` and `--config` work here too: they say where the profile goes, not what is in it.

The wizard needs a terminal *and* an otherwise-bare command line. Passing any configuring flag, or running without a TTY — CI, a pipe, an agent — takes the explicit form below, unchanged:

```
notion-track init --data-source-id <id> --ticket-prop <name> --status-prop <name> --title-prop <name> [--due-prop <name>] [--assignee-prop <name>] [--priority-prop <name>] [--id-prop <name>] [--me <value>] [--database-id <id>] [--list]
```

| Flag | Meaning |
|---|---|
| `--data-source-id string` | data source id (required, unless `--list`) |
| `--ticket-prop string` | property holding the ticket key — must be `rich_text` or `title` (required) |
| `--status-prop string` | property holding the status — must be `status` or `select` (required) |
| `--title-prop string` | title property (required) |
| `--due-prop string` | date property (optional) |
| `--assignee-prop string` | `select` property naming who owns the row (optional) |
| `--priority-prop string` | `select` property ranking how urgent the row is (optional) |
| `--id-prop string` | `unique_id` property holding the row's board id, e.g. `BDF-271` (optional) |
| `--me string` | the value `--assignee me` resolves to; resolved and validated against `--assignee-prop`'s options before being saved (optional, needs `--assignee-prop`) |
| `--database-id string` | database id, recorded for reference only — every read/write is keyed off `--data-source-id`, not this |
| `--list` | list the data source ids shared with the integration, and exit |

Each mapped property is checked against the data source's live schema; `init` refuses to write a profile that would break on first use (wrong type, or a property that doesn't exist). `--ticket-prop`, `--status-prop`, and `--title-prop` are required in practice — `init` returns a usage error naming which one is missing — even though `--due-prop`, `--assignee-prop` and `--priority-prop` are optional. The profile is written under the name given by `--profile` (default `"default"`); if this is the first profile in the file it also becomes `default_profile`. Running `init` again with the same `--profile` name overwrites that profile without touching the others.

`--assignee-prop` behaves like `--due-prop`: a board that tracks nobody in particular simply leaves it unmapped, and every command behaves exactly as it did before this feature. `--me` resolves its value against `--assignee-prop`'s options the same way `--assignee me` does, so a typo can't reach the file, and saves the canonical name — but because `config.yml` is meant to be committed and shared, `init --me` prints a warning recommending `NOTION_TRACK_ME` instead of relying on the value it just wrote (see [Environment variables](#environment-variables)).

`--priority-prop` behaves like `--due-prop` too: a board with no notion of urgency simply leaves it unmapped, and every command behaves exactly as it did before this feature. Unlike `--assignee-prop`, there is no `--priority-me` equivalent — a priority belongs to no one, so there is no identity to resolve it against.

`--id-prop` maps Notion's own row identifier — a `unique_id` column, the kind that renders as `BDF-271` on the board — so rows can be addressed by that short id instead of by ticket key or page id (see `--id` under `get` and `set` below). It behaves like `--due-prop`: a board with no such column simply leaves it unmapped, and `--id` is then unavailable — the row is still reachable the other two ways. `init` requires the mapped property to actually be `unique_id`, the same way `--ticket-prop` requires `rich_text` or `title`.

**Token prompt.** If no token is found in `NOTION_TOKEN` or `credentials.yml`, `init` behaves differently depending on how it's run:

- **At an interactive terminal**, it asks for the token (input isn't echoed to the screen) and offers to save it to `credentials.yml` — bare Enter accepts the recommended default. Decline and it prints the `export NOTION_TOKEN=...` line to run for the current session instead, without ever printing the token itself: a child process has no way to modify its parent shell's environment, so this is the closest it can get to doing that for you.
- **Non-interactively** — CI, a pipe, a script, an agent — it never prompts. Same as every other command: exit code 5 and a message pointing at `NOTION_TOKEN`.

### `upsert` — create or update a row by ticket key

```
notion-track upsert --ticket <key> [--title <title>] [--status <status>] [--due YYYY-MM-DD] [--assignee <value>] [--unassign] [--priority <value>] [--json]
```

The flagship command. Queries the data source for the row whose ticket property equals `--ticket`: updates it if found, creates it otherwise. `0` matches → create, `1` match → update, `>1` matches → fails with exit code 4 (see [Limitations](#limitations)). Silent on success; with `--json` it prints `{"action": "created"|"updated", "page": {...}}`.

### `set` — update an existing row only

```
notion-track set (--ticket <key> | --id <board-id> | --page-id <id>) [--title <title>] [--status <status>] [--due YYYY-MM-DD] [--assignee <value>] [--unassign] [--priority <value>] [--json]
```

Same fields as `upsert`, but fails with exit code 3 if the row doesn't exist yet, instead of creating it. Use this where a missing row is a symptom worth surfacing rather than something to paper over.

```bash
notion-track set --id BDF-271 --status "Done"
```

`--ticket`, `--id` and `--page-id` are mutually exclusive and exactly one is required. `--page-id` addresses a row directly by its Notion page id — no query by ticket key at all — which is faster and unambiguous when you already have it (e.g. from a prior `--json` call's `page_id`, see [JSON output](#json-output)). It accepts the full page URL you'd copy out of Notion's browser address bar, a bare 32-character hex id, or a dashed UUID; any other input fails immediately with exit code 2, before any request is made. Because `GET`ting a page by id works for anything shared with the integration — not just rows of the configured data source — a page id that resolves to a *different* data source than the active profile is rejected with exit code 2 rather than left to fail later with a confusing property-name error from Notion. `set --page-id` also rejects, with the same exit code, a page whose parent carries no data source at all — its membership can never be confirmed, and a write must not proceed on a page that cannot prove it belongs to this profile.

`--id` addresses a row by its **board id** — the short identifier Notion shows on the row and the one people read aloud (`BDF-271`, or the bare number `271` on its own) — resolved with a query against the mapped `unique_id` column, the same way `--ticket` resolves against the ticket property; Notion's API filters on `unique_id` natively, so this needs no client-side scan. It needs a `unique_id` property mapped first (`init --id-prop`, see `init` above); without one mapped, `--id` fails with exit code 2 — the same class of mistake as running before `notion-track init` — naming the fix. An empty `--id` fails the same way, before any request is made. A malformed `--id` — the wrong prefix, or not a number at all — is exit code 2 too, but not that early: telling a bad prefix from a good one needs the data source's schema first, so a malformed `--id` costs one request before it fails, short of the row query itself.

### `--assignee` / `--unassign` — set or clear who owns a row

```bash
notion-track set --ticket BDF-231 --assignee "Mirko Spinato"
notion-track set --ticket BDF-231 --assignee mirko    # a partial name is enough when it's unambiguous
notion-track set --ticket BDF-231 --assignee me        # NOTION_TRACK_ME, or the profile's `me:` — see below
notion-track set --ticket BDF-231 --unassign            # clears the column
```

Available on `upsert` and `set`. `--assignee` resolves what you type against the mapped column's options, trying an exact match, then an exact case-insensitive match, then a case-insensitive substring match, and stopping at whichever pass finds exactly one candidate — so `mirko` reaches Notion as `Mirko Spinato`. Zero matches and more than one are both usage errors (exit code 2): the first names the values the column actually offers, the second names which ones matched and asks for more of the name.

`me` is a reserved value: before resolution runs, it is replaced by `NOTION_TRACK_ME` (or, failing that, the profile's `me:` field — see [Environment variables](#environment-variables) for why the environment variable is the one to actually use), so `NOTION_TRACK_ME=marco` works exactly like typing the full name. Using `me` with neither configured is a usage error naming the fix.

Not passing `--assignee` at all leaves the column untouched — the same "empty means leave it alone" rule every other field follows. `--assignee ""` is therefore a usage error, not a way to clear the column; use `--unassign` for that. `--assignee` and `--unassign` are mutually exclusive, and a select column holds one value, so `--assignee` cannot be repeated.

If the role isn't mapped, passing `--assignee` or `--unassign` fails the same way any other unmapped role does — exit code 1, not 2, see [Exit codes](#exit-codes) — with a message pointing at `init --assignee-prop`.

### `--priority` — how urgent a row is

```bash
notion-track set --ticket BDF-231 --priority ALTA
notion-track set --ticket BDF-231 --priority alta    # a partial value is enough when it's unambiguous
notion-track list --priority ALTA
```

Available on `upsert` and `set` to write it, and on `list` to filter by it. `--priority` resolves what you type against the mapped column's options the same way `--assignee` does: an exact match, then an exact case-insensitive match, then a case-insensitive substring match, stopping at whichever pass finds exactly one candidate — so `alta` reaches Notion as `ALTA`. Zero matches and more than one are both usage errors (exit code 2), naming the values the column actually offers or which ones matched, exactly like `--assignee`.

Not passing `--priority` at all leaves the column untouched — the same "empty means leave it alone" rule every other field follows.

If the role isn't mapped, passing `--priority` fails the same way any other unmapped role does — exit code 1, not 2, see [Exit codes](#exit-codes) — with a message pointing at `init --priority-prop`.

**What it does not have, unlike `--assignee`:** there is no `--unpriority` flag — nothing in this tool can clear a priority once set; that has to be done in Notion. There is no `list --unprioritized` to find rows with none, the way `--unassigned` does for the assignee. And there is no `me`-like reserved value: a priority belongs to no one, so there is no identity to resolve.

### `--body-file` — write the page body from Markdown

```
notion-track upsert --ticket <key> --body-file notes.md
notion-track set --page-id <id> --body-file -
```

Available on both `upsert` and `set`. `--body-file` takes a path to a Markdown file, or `-` to read from stdin; its content becomes the row's Notion page body, converted to native blocks. Properties (`--title`, `--status`, `--due`) and the body are independent — pass both, either, or neither.

**Replace semantics.** `--body-file` **owns the page body**: every run makes the body equal to the file's content, deleting whatever blocks were already there — including anything added by hand in Notion since the last run. Running it twice on the same file yields the same body, not a duplicate. There is no append mode and no undo, so treat the file as the single source of truth for that page and read the page first (`get`) if you're not sure what's there. Sub-pages and child databases nested under the page are never touched — they're skipped rather than archived, and a warning on stderr names each one that was kept.

**Supported Markdown.** Headings (`#`/`##`/`###`, deeper levels flatten to h3), paragraphs, bulleted and numbered lists, task checkboxes (`- [ ]` / `- [x]`), fenced and indented code blocks, blockquotes, `---` dividers, and inline **bold**, *italic*, `code`, ~~strikethrough~~, and links. List and quote nesting is supported to 2 levels. Tables, images, raw HTML, and nesting past 2 levels aren't dropped — each **degrades** to the closest supported block (a table becomes a plain-text code block, an image becomes a link, deeper nesting is promoted up a level) and prints a warning to stderr naming what happened, so nothing silently disappears but nothing blocks the write either. A file over 1 MiB is rejected before any request is made (exit code 2).

**Cost.** There's no bulk-delete endpoint in Notion's API, so replacing a body is `O(n)` in the number of blocks already on the page: append the new content, then delete the old blocks one by one. A page with a lot of existing content takes correspondingly longer, and `notion-track` prints progress lines to stderr (blocks appended, blocks deleted so far) so a long run doesn't look hung.

**Placeholders (`--expand`).** With `--expand`, `{{ticket}}` and `{{date}}` in the body file are substituted before the file is parsed — `{{date}}` being today, as `YYYY-MM-DD`. Whitespace inside the braces is fine (`{{ ticket }}`).

```bash
notion-track upsert --ticket BDF-231 --body-file release-notes.md --expand
```

A placeholder nothing can fill in is a usage error naming the line, rather than a body reaching Notion with a literal `{{tikcet}}` in it that nobody notices until they read the page. Expansion is off by default and there is no escape syntax: a body that legitimately contains braces — a document about templating, a snippet of Handlebars — simply does not pass the flag. Addressing a row with `--page-id` or `--id` leaves `{{ticket}}` empty, since no ticket key was given.

**Concurrency.** Two `--body-file` runs against the same page racing each other can both append before either deletes, leaving the body duplicated — there's no lock to take on a Notion page. Don't run concurrent body writes against one page.

With `--json`, a successful write adds a `body` object: `{"blocks_written": N, "blocks_deleted": N}`. If the properties write succeeds but the body replace fails partway, the command still exits 1 (not 0), and `--json` prints `body: {"written": false, "error": "...", "blocks_written": N, "blocks_deleted": N}` — `written` tells you the body is *not* in the state the file describes, while `page` in the same output still reflects whatever properties were applied, since those are two separate Notion API calls and the first can succeed even if the second doesn't.

### `get` — read one row

```
notion-track get (--ticket <key> | --id <board-id> | --page-id <id>) [--json]
```

```bash
# by exact title (or ticket key)
notion-track get --ticket "Sistemare visualizzazione da telefono"

# by board id, the one people say out loud
notion-track get --id BDF-271

# by Notion page id or URL, stable across renames
notion-track get --page-id https://notion.so/...
```

Prints the row's board id (when mapped), ticket, title, status and URL. `--ticket`, `--id` and `--page-id` are mutually exclusive and exactly one is required — see `set` above for what `--id` and `--page-id` accept and how they're validated. Fails with exit code 3 if not found (Notion's 404 doesn't distinguish "no such page" from "never shared with this integration" — the error message says so), 4 if a ticket key matches more than one row (see [Limitations](#limitations)), or 2 for a malformed `--id` or `--page-id`, an id role that isn't mapped, or a page id outside the active profile's data source. Unlike `set`, `get --page-id` accepts a page whose parent carries no data source at all — a read cannot do any harm with an unconfirmed page the way a write could.

### `list` — read many rows

```
notion-track list [--status <status>] [--assignee <value>] [--unassigned] [--priority <value>] [--json]
```

Lists every row, or narrows it by `--status`, by `--assignee`, to `--unassigned` rows, or by `--priority` — `--assignee` and `--unassigned` are mutually exclusive. An unknown status, assignee or priority value fails fast with exit code 2, naming the values Notion actually allows for that property; `--assignee` resolves partial names and `me` exactly as it does on `upsert`/`set`, and `--priority` resolves partial values the same way (see `--assignee` / `--unassign` and `--priority` under [Usage](#usage) above). Filtering by assignee or priority on a profile that doesn't map the role fails like any other unmapped role (exit code 1). Unlike `--unassigned`, there is no `--unprioritized` to find rows with no priority.

The human-readable form appends `  !<value>` and `  @<name>` to a row that has them; rows with neither, and every row on a profile that doesn't map either role at all, print exactly as they did before this feature. When the id role is mapped, each row is also prefixed with its board id, the same way `get` shows it.

When nothing matches, the human-readable form prints `no matching tasks` **to stderr** and exits 0 — stdout stays empty, so `list | wc -l` counts rows and nothing else. `list --json` prints `[]` and says nothing on stderr.

### `apply` — many writes from one manifest

```
notion-track apply --file tasks.json [--dry-run] [--expand] [--json]
```

Applies a list of writes from a JSON or CSV file, one entry at a time, in order. The format is chosen from the extension.

```json
[
  {"op": "upsert", "ticket": "BDF-1", "title": "Hardening", "status": "In progress", "assignee": "mirko", "priority": "alta"},
  {"op": "set", "ticket": "BDF-2", "status": "Done", "unassign": true}
]
```

```csv
op,ticket,title,status,due,body_file,assignee,unassign,priority
upsert,BDF-1,Hardening,In progress,2026-08-01,notes.md,mirko,,alta
set,BDF-2,,Done,,,,true,
```

Fields: `op` (`upsert` or `set`, defaulting to `upsert` — the idempotent one, so a manifest run twice by mistake leaves the board as it was), `ticket` (required), `title`, `status`, `due`, `body_file`, `assignee`, `unassign`, `priority`. An unknown field is an error rather than something quietly ignored: a manifest with `stuats` in it would otherwise leave every row's status unset and say nothing.

`assignee` accepts the same partial names and the reserved `me` that `--assignee` does; `unassign` accepts `true`/`false`/empty (case-insensitive) and is registered in both formats, so it is just as legal in CSV as in JSON. Passing both `assignee` and `unassign: true` on the same entry is rejected the same way `--assignee` and `--unassign` are on the flags. `priority` accepts the same partial values that `--priority` does; there is no `unpriority` field, the same way there is no `--unpriority` flag.

`body_file` paths are resolved **relative to the manifest**, not to your working directory, so a manifest and the files it names travel together.

**It stops at the first entry that fails**, reports which one and how many were applied, and exits with that entry's own code — so a pipeline branching on 3 (not found) or 4 (duplicate) still learns why the run stopped. Entries are applied sequentially, never in parallel: two writes racing on the same ticket key can create a duplicate, and a manifest is exactly where the same key is most likely to appear twice.

```
1/3 upsert BDF-1 updated
2/3 upsert BDF-2 failed: unknown status "Nonexistent"; allowed values are: To do, In progress, Done
stopped at entry 2 of 3: 1 applied, 2 not applied
```

`--dry-run` and `--expand` work here exactly as they do on `upsert` and `set`, which makes `apply --dry-run` the way to check a manifest before running it for real.

### `--dry-run` — see what a write would do

```bash
notion-track upsert --ticket BDF-231 --status Done --dry-run
```

```
would update 1f2e3d4c-...
  Ticket               BDF-231
  Stato                Done
  https://notion.so/...
```

Available on `upsert` and `set`. It reports whether the row would be created or updated, which row, and which columns would be written — and writes nothing. With `--json` the output is `{"dry_run": true, "plan": {...}}`, so a script can tell it apart from a write that actually happened.

`--unassign --dry-run` prints a `clear` line naming the column instead of a value — without it, clearing the assignee would be the one write a dry run has nothing to say about, the most destructive one in this feature and invisible in the very command that exists to show it:

```
$ notion-track set --ticket BDF-231 --unassign --dry-run
would update 1f2e3d4c-...
  clear                Referente
  https://notion.so/...
```

"Without touching the API" can only mean without *writing*: whether a ticket key resolves to a create or an update, and whether a status value even exists, are questions only the live data source can answer. A dry run therefore makes the same reads a real run does and stops before the first write — including the same validation, so a status your board would reject fails now rather than on the run you were about to do for real.

### `doctor` — check the setup

```
notion-track doctor [--json]
```

Runs five checks — `token`, `data_source`, `properties`, `duplicates`, `secrets` — plus a sixth, `assignee`, between `properties` and `duplicates`, when the role is mapped; each prints as `ok`, `warn`, or `fail` with an actionable detail message. A `warn` (e.g. the status property's type changed since `init` ran) does not fail the command; any `fail` makes it exit non-zero. Of the checks that talk to Notion, only `duplicates` still runs when `data_source` fails — it doesn't need the schema, so a broken setup at least gets scanned for duplicate ticket keys instead of stopping there; `properties` and `assignee` both need the live schema, so a `data_source` failure skips them until it's fixed. (`secrets` also still runs, but it never talks to Notion in the first place — see below.)

`assignee` verifies that the configured identity (`me:`, or `NOTION_TRACK_ME`) still resolves to an option the mapped column offers — an option renamed in Notion would otherwise turn every `--assignee me` into a runtime failure discovered only when a write is attempted. It only ever reports `ok` or `warn`, never `fail`, and it also warns when the identity comes from `me:` in the config file rather than `NOTION_TRACK_ME` — see [Environment variables](#environment-variables) for why that distinction matters.

`secrets` is the only check that looks at your machine rather than at Notion: it scans the files the current git repository *tracks* for anything shaped like an integration token, and warns with the file and line number — never with the matched text, which would leak the secret a second time into scrollback and CI logs. Untracked files are left alone: a token in an ignored `.env` is not the mistake this is for. Running outside a repository, or without git installed, reports `ok` with the reason rather than a warning nobody can act on.

## Configuration

`notion-track` keeps two files side by side in `os.UserConfigDir()/notion-track/` — respecting `$XDG_CONFIG_HOME` on Linux:

| File | Holds | Safe to commit? |
|---|---|---|
| `config.yml` | profiles: data source id, property mapping | Yes — no secret |
| `credentials.yml` | the integration token | **No — never commit this** |

They're two files, not one, for exactly this reason: `config.yml` is meant to be committed to a project repo so CI and every teammate share the same property mapping (see [CI usage](#ci-usage)); `credentials.yml` holds the one thing that must never end up in that repo. Splitting them makes "the token can't leak through the committed config" a property of the file layout, not a rule someone has to remember while editing YAML.

| OS | Default directory |
|---|---|
| macOS | `~/Library/Application Support/notion-track/` |
| Linux | `~/.config/notion-track/` |
| Windows | `%AppData%\notion-track\` |

Pass `--config /path/to/file.yml` to point `config.yml` at a different file entirely — this is how you use a config file **committed to a project repo** instead of the per-user default (see [CI usage](#ci-usage)). There is no equivalent flag for `credentials.yml`: it is deliberately always the per-user, per-machine default location, never something a project repo points at.

```yaml
# config.yml
schema_version: 1        # written by `init`; don't hand-edit it
default_profile: work    # used when --profile and NOTION_TRACK_PROFILE are both unset
profiles:
  work:
    database_id: "1a2b3c4d..."     # optional, informational only — nothing reads it
    data_source_id: "5e6f7a8b..."  # required — every query, create and update is keyed off this
    status_type: status            # "status" or "select", recorded by `init`; see doctor's "properties" check
    properties:
      ticket: Ticket        # rich_text or title property holding the ticket key
      status: Status        # status or select property
      title: Name           # title property
      due: Due              # optional: date property
      assignee: Referente   # optional: select property naming who owns the row
      priority: Urgenza     # optional: select property ranking how urgent the row is
      id: ID                # optional: unique_id property holding the row's board id
    me: Marco Arnulfo       # optional: the value `--assignee me` resolves to; NOTION_TRACK_ME overrides it
```

```yaml
# credentials.yml — never commit this file
schema_version: 1
token: ntn_...
```

Both files are replaced atomically (a temporary file in the same directory, then a rename), but only `credentials.yml` is guaranteed `0600`: its temp file has a random suffix and its permissions are set explicitly, immune to anything already sitting at a guessable temp path. `config.yml`'s temp file has a fixed name and its permissions are not forced onto a pre-existing file there, so a leftover `config.yml.tmp` from an earlier run can leave it at whatever mode that leftover already had (e.g. `0644`) — acceptable only because, unlike `credentials.yml`, it holds no secret of its own.

`credentials.yml` is written in exactly one place: `init`, when it runs at an interactive terminal, finds no token in `NOTION_TOKEN` or the file already, and you accept the "save it?" prompt (the default — see [Quick start](#quick-start)). Nothing writes a token to `config.yml`, ever.

### Environment variables

| Variable | Effect |
|---|---|
| `NOTION_TOKEN` | the integration token. Always wins over `credentials.yml` when set — this is what lets CI pass a token that never touches disk. |
| `NOTION_TRACK_PROFILE` | which profile to resolve, unless `--profile` is also given |
| `NOTION_TRACK_DB` | overrides the resolved profile's `database_id` |
| `NOTION_TRACK_DATA_SOURCE` | overrides the resolved profile's `data_source_id` |
| `NOTION_TRACK_ME` | overrides the resolved profile's `me:` — the value `--assignee me` resolves to |

Precedence:

- **Profile selection:** `--profile` flag → `NOTION_TRACK_PROFILE` → `default_profile` in the config file.
- **`database_id` / `data_source_id`:** the env vars above always override whatever the resolved profile has on file, regardless of how that profile was chosen — this is what lets a CI job point an existing profile at a different data source without touching the committed file.
- **Identity (`--assignee me`):** `NOTION_TRACK_ME` → the profile's `me:` field, same override mechanism as `database_id`/`data_source_id` above. The environment variable is the one to actually use: `config.yml` is meant to be committed and shared (see [Configuration](#configuration)), so a `me:` written there is *everyone's* identity — a teammate who never exports `NOTION_TRACK_ME` resolves `me` to whoever committed the file, silently assigning work to the wrong person. `init --me` prints a warning to that effect the moment it writes one, and `doctor` warns if a profile has `me:` set but `NOTION_TRACK_ME` isn't.
- **Token:** `NOTION_TOKEN` → `credentials.yml`. A token read from the environment is never written back to `credentials.yml` — a CI secret can never leak onto disk through a normal run. Run `notion-track doctor` if you need to see which source actually won.
- **Config file location:** `--config` flag → the OS default path above. There is no environment variable for the path itself, and no equivalent flag for `credentials.yml`.

## JSON output

Every `--json` shape below is a **documented, stable scripting contract**: a key is never renamed or removed without a breaking-change announcement.

A row (`get --json`, and each entry of `list --json`):

```json
{
  "id": "BDF-271",
  "ticket": "BDF-231",
  "title": "Hardening",
  "status": "In progress",
  "page_id": "1a2b3c4d-...",
  "url": "https://www.notion.so/...",
  "last_edited_time": "2026-07-23T10:15:00Z",
  "assignee": "Mirko Spinato",
  "priority": "ALTA"
}
```

`id` comes first because it is the row's identity, the same order the board displays it in. If the configured property mapping names a column the row doesn't actually carry, the corresponding field comes back as an empty string rather than an error — a broken mapping is `doctor`'s job to report, not a reason to fail every read. `id` follows the same rule as `assignee` and `priority` below: always present, empty whenever the row carries no value or the role isn't mapped, so a script never has to branch on whether the key is present — only on whether it's empty. `assignee` follows the same rule and is additionally empty whenever nobody is assigned. `priority` follows the same rule too: always present, empty whenever the row carries no value or the role isn't mapped.

`upsert --json` / `set --json`:

```json
{
  "action": "created",
  "page": { "id": "BDF-271", "ticket": "BDF-231", "title": "Hardening", "status": "In progress", "page_id": "...", "url": "...", "last_edited_time": "...", "assignee": "Mirko Spinato", "priority": "ALTA" }
}
```

`action` is `"created"` or `"updated"`.

`doctor --json` — an array of checks, one per `token` / `data_source` / `properties` / `assignee` (only when the role is mapped) / `duplicates` / `secrets`:

```json
[
  { "name": "token", "status": "ok", "detail": "token from environment\n  authenticated as notion-track" },
  { "name": "data_source", "status": "ok", "detail": "reachable: Tasks" },
  { "name": "properties", "status": "ok", "detail": "all mapped properties exist with the expected types" },
  { "name": "assignee", "status": "ok", "detail": "--assignee me resolves to Mirko Spinato" },
  { "name": "duplicates", "status": "ok", "detail": "42 rows, no repeated ticket keys" },
  { "name": "secrets", "status": "ok", "detail": "37 tracked files scanned, no token-looking strings" }
]
```

`status` is `"ok"`, `"warn"`, or `"fail"`; `detail` is omitted only when empty, which does not happen in practice.

## Exit codes

Pipelines can branch on these without parsing any message text:

| Code | Name | Meaning |
|---|---|---|
| `0` | OK | success |
| `1` | Error | a generic failure — a network/API error, `doctor` reporting a failed check other than `token`, or a value passed for `--assignee`, `--priority` or `--due` when that role isn't mapped in the active profile (an unmapped `id` role is the one exception — it's exit code 2, see below) |
| `2` | Usage | the invocation cannot work as written: a missing/invalid flag, more than one of `--ticket`/`--id`/`--page-id` given or none of the three given, an unknown command, no config yet (`notion-track init` was never run), a status value the data source doesn't allow, a malformed `--page-id` or `--id`, an `--id` used on a profile with no id role mapped, a `--page-id` that resolves outside the active profile's data source, an `--assignee` value that resolves to zero or more than one option, an empty `--assignee`, `--assignee me` with no configured identity, `--assignee` combined with `--unassign` (or with `--unassigned` on `list`), a `--priority` value that resolves to zero or more than one option, or an empty `--priority` |
| `3` | Not found | the requested ticket, board id, or page id has no matching row (or, for a page id, one not shared with this integration) (`get`, `set`) |
| `4` | Duplicate | the ticket key matches more than one row (`upsert`, `set`, `get`) |
| `5` | Auth | no token was found (including a `credentials.yml` that exists but can't be read), or Notion rejected it (401/403) — including `doctor`, when its `token` check is the only one that failed |

## CI usage

Because the config file has no secret in it, the common pattern is to **commit it to the repository** and point at it explicitly with `--config`, while the token comes from a CI secret:

```yaml
# .github/workflows/notion.yml
- name: Install notion-track
  uses: marcoarnulfo/notion-cli/action@main
  with:
    version: v0.6.0   # or "latest"

- name: Mark the ticket done
  run: notion-track upsert --ticket "$TICKET" --status "Done" --config notion-track.yml
  env:
    NOTION_TOKEN: ${{ secrets.NOTION_TOKEN }}
    TICKET: ${{ github.event.inputs.ticket }}
```

The action downloads the release archive for the runner it is on, checks it against the release's `checksums.txt`, and puts the binary on `PATH` — no Go toolchain, no compile. Linux, macOS and Windows runners (Windows through Git Bash), amd64 and arm64; anywhere else it fails with a message that says so. It needs a published release to download, so until the first tag exists use `go install github.com/marcoarnulfo/notion-cli/cmd/notion-track@latest` instead.

`@main` is a moving reference: you get whatever is on the branch at the time. Pin it to a commit SHA if you want a workflow that cannot change under you — a `@v1` tag will exist once this project reaches 1.0.

## Limitations

These are current, deliberate tradeoffs — not bugs to be surprised by:

1. **Every change is attributed to the integration, not to you.** Notion records edits made through the API as made by the integration's bot identity. If you check a page's edit history in Notion, you will see the integration's name, never the human or CI job that ran the command.
2. **`upsert` and `get` fail on duplicate ticket keys instead of picking one.** If more than one row shares the same ticket key, `notion-track` refuses to guess which one you meant — it exits with code 4 and lists the offending rows. Run `notion-track doctor` to find and clean them up.
3. **Two concurrent jobs creating the same new ticket can race into a duplicate.** `upsert`'s create-or-update decision reads the current rows, then writes; the Notion API offers no unique-constraint or compare-and-swap primitive to close that window. This is not preventable client-side — `doctor`'s duplicate scan is the mitigation, not a fix.
4. **Only a Workspace Owner can set this up.** Creating the integration and sharing a database with it both require Workspace Owner permissions in Notion. A workspace guest — one of the reasons this tool exists in the first place — cannot do either step, but can use the tool freely once someone with Owner rights has.
5. **`--body-file` replaces the whole page body, with no lock and no undo.** It owns the body: each run overwrites everything there, hand-edited content included, and two runs racing the same page can duplicate it. See `--body-file` under [Usage](#usage) above.

## Use it from an AI agent

Because the tool is quiet on success, speaks `--json` with a stable schema, and returns [differentiated exit codes](#exit-codes), an agent can drive it as reliably as a script does — no scraping of human output. A ready-made [Claude Code](https://claude.com/claude-code) skill lives in **[`skills/notion-track/`](skills/notion-track/)**: it teaches an agent which command to reach for and how to stay safe (read before writing, never invent a status, branch on exit codes). Install it by copying its `SKILL.md` into `~/.claude/skills/notion-track/`, then ask your agent to "mark that task done on Notion". For hosts that speak MCP rather than the shell, **`notion-track mcp`** serves the same operations as tools over stdio:

```json
{
  "mcpServers": {
    "notion-track": { "command": "notion-track", "args": ["mcp"] }
  }
}
```

It exposes `upsert_task`, `set_task`, `get_task` and `list_tasks`, returning the same JSON shape documented above. `upsert_task` and `set_task` accept `assignee` and `unassign` exactly like the CLI's flags do — a partial name, or the reserved `me`; `list_tasks` accepts `assignee` and `unassigned`, mutually exclusive with each other. `upsert_task` and `set_task` also accept `priority`, resolved the same way `--priority` is — a partial value is enough when unambiguous; `list_tasks` accepts `priority` too, narrowing to rows carrying that value. There is no `unpriority` argument, the same way the CLI has no `--unpriority` flag. It is an adapter, not a second implementation: every tool reaches the same code the CLI commands do, so the duplicate check, the status validation and the property mapping behave identically for an agent. `stdout` carries the JSON-RPC protocol and nothing else.

Addressing is the one place the two surfaces diverge: over MCP a row is found **only** by ticket key — `get_task` and `set_task` take a `ticket` argument and nothing else. The CLI's `--id` and `--page-id` have no MCP equivalent, even though the JSON a tool returns still carries the board id under `id`, same as `--json` does on the CLI.

This does not contradict the reason this tool exists. Notion's *hosted* MCP endpoint is the one blocked by corporate firewalls; a *local* server, running on your machine with your own integration token, reaches agents exactly where the hosted one cannot.

## Contributing

Contributions are welcome — this is a free, open-source project. See **[CONTRIBUTING.md](CONTRIBUTING.md)** for the development setup, the checks to run before opening a PR, and the project's non-negotiable architectural rules. Please also read the [Code of Conduct](CODE_OF_CONDUCT.md). Found a security issue? See [SECURITY.md](SECURITY.md) instead of opening a public issue.

## Roadmap

Implemented today: `init` (interactive wizard and flag-driven, with `--list`), the browsing TUI, `upsert`, `set`, `get`, `list`, `doctor`; `--dry-run` on `upsert`/`set`; `apply` for bulk writes from a manifest; `--body-file` on `upsert`/`set` to write the page body from Markdown, with `--expand` for `{{ticket}}`/`{{date}}` placeholders; `--json` on every command that produces output; `mcp` to serve the same operations as MCP tools; an optional `assignee` role with `--assignee`/`--unassign`, `list --assignee`/`--unassigned` and the `me` identity; an optional `priority` role with `--priority` on `upsert`/`set`/`list`; an optional `id` role mapped with `init --id-prop`, addressing a row by its Notion board id with `--id` on `get`/`set`; profiles; retry with backoff.

Built but not yet exercised: the **GoReleaser pipeline** (`.goreleaser.yaml` plus a release workflow triggered on `v*` tags) and the **composite GitHub Action** in [`action/`](action/). Both are in place and the pipeline has been verified locally with `goreleaser release --snapshot`, but neither has run for real: that happens when the first tag is pushed, and until then the releases page is empty and the action has nothing to download.

## License

[MIT](LICENSE)
