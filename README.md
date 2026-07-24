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
- **Read** (`get`, `list`) — one row or many, optionally filtered by status, human-readable or `--json`.
- **Diagnostics** (`doctor`) — checks the token, data source access, the property mapping (including type drift since `init`), and scans the whole data source for duplicate ticket keys.
- **Guided setup** (`init`) — writes a profile from flags, validated against the data source's live schema before anything is saved; `init --list` discovers the data source ids your integration can see. At an interactive terminal, it also offers to collect and save the integration token if none is found (see [Configuration](#configuration)).
- **Profiles** — several named database configurations in one YAML config file, selectable by flag, environment variable, or a configured default.
- **`--json` everywhere** — every command that produces output (`get`, `list`, `doctor`, `upsert`, `set`) can emit machine-readable JSON with a documented, stable shape.
- **CI-friendly by design** — quiet on success, a distinct exit code per failure class (auth, not found, duplicate, usage, generic), no interactive prompts.
- **Retries with backoff** on Notion's rate limiting (429) and transient 502/503/504/529 responses, honoring `Retry-After` when Notion sends one.
- **A single static Go binary** — no Node runtime, no Python venv.

## Requirements

- **[Go](https://go.dev/dl/) 1.26 or newer** — needed to build or install from source; prebuilt binaries are not published yet (see [Roadmap](#roadmap)).
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

There is no `go get`-able prebuilt binary release yet — GoReleaser and GitHub Releases are on the [roadmap](#roadmap).

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

## Usage

Global flags, available on every command:

| Flag | Meaning |
|---|---|
| `--profile string` | config profile to use (see [Configuration](#configuration)) |
| `--config string` | path to an explicit config file, instead of the default OS location |

### `init` — configure a profile

```
notion-track init --data-source-id <id> --ticket-prop <name> --status-prop <name> --title-prop <name> [--due-prop <name>] [--database-id <id>] [--list]
```

| Flag | Meaning |
|---|---|
| `--data-source-id string` | data source id (required, unless `--list`) |
| `--ticket-prop string` | property holding the ticket key — must be `rich_text` or `title` (required) |
| `--status-prop string` | property holding the status — must be `status` or `select` (required) |
| `--title-prop string` | title property (required) |
| `--due-prop string` | date property (optional) |
| `--database-id string` | database id, recorded for reference only — every read/write is keyed off `--data-source-id`, not this |
| `--list` | list the data source ids shared with the integration, and exit |

Each mapped property is checked against the data source's live schema; `init` refuses to write a profile that would break on first use (wrong type, or a property that doesn't exist). `--ticket-prop`, `--status-prop`, and `--title-prop` are required in practice — `init` returns a usage error naming which one is missing — even though `--due-prop` is the only optional one. The profile is written under the name given by `--profile` (default `"default"`); if this is the first profile in the file it also becomes `default_profile`. Running `init` again with the same `--profile` name overwrites that profile without touching the others.

**Token prompt.** If no token is found in `NOTION_TOKEN` or `credentials.yml`, `init` behaves differently depending on how it's run:

- **At an interactive terminal**, it asks for the token (input isn't echoed to the screen) and offers to save it to `credentials.yml` — bare Enter accepts the recommended default. Decline and it prints the `export NOTION_TOKEN=...` line to run for the current session instead, without ever printing the token itself: a child process has no way to modify its parent shell's environment, so this is the closest it can get to doing that for you.
- **Non-interactively** — CI, a pipe, a script, an agent — it never prompts. Same as every other command: exit code 5 and a message pointing at `NOTION_TOKEN`.

### `upsert` — create or update a row by ticket key

```
notion-track upsert --ticket <key> [--title <title>] [--status <status>] [--due YYYY-MM-DD] [--json]
```

The flagship command. Queries the data source for the row whose ticket property equals `--ticket`: updates it if found, creates it otherwise. `0` matches → create, `1` match → update, `>1` matches → fails with exit code 4 (see [Limitations](#limitations)). Silent on success; with `--json` it prints `{"action": "created"|"updated", "page": {...}}`.

### `set` — update an existing row only

```
notion-track set (--ticket <key> | --page-id <id>) [--title <title>] [--status <status>] [--due YYYY-MM-DD] [--json]
```

Same fields as `upsert`, but fails with exit code 3 if the row doesn't exist yet, instead of creating it. Use this where a missing row is a symptom worth surfacing rather than something to paper over.

`--ticket` and `--page-id` are mutually exclusive and exactly one is required. `--page-id` addresses a row directly by its Notion page id — no query by ticket key at all — which is faster and unambiguous when you already have it (e.g. from a prior `--json` call's `page_id`, see [JSON output](#json-output)). It accepts the full page URL you'd copy out of Notion's browser address bar, a bare 32-character hex id, or a dashed UUID; any other input fails immediately with exit code 2, before any request is made. Because `GET`ting a page by id works for anything shared with the integration — not just rows of the configured data source — a page id that resolves to a *different* data source than the active profile is rejected with exit code 2 rather than left to fail later with a confusing property-name error from Notion. `set --page-id` also rejects, with the same exit code, a page whose parent carries no data source at all — its membership can never be confirmed, and a write must not proceed on a page that cannot prove it belongs to this profile.

### `--body-file` — write the page body from Markdown

```
notion-track upsert --ticket <key> --body-file notes.md
notion-track set --page-id <id> --body-file -
```

Available on both `upsert` and `set`. `--body-file` takes a path to a Markdown file, or `-` to read from stdin; its content becomes the row's Notion page body, converted to native blocks. Properties (`--title`, `--status`, `--due`) and the body are independent — pass both, either, or neither.

**Replace semantics.** `--body-file` **owns the page body**: every run makes the body equal to the file's content, deleting whatever blocks were already there — including anything added by hand in Notion since the last run. Running it twice on the same file yields the same body, not a duplicate. There is no append mode and no undo, so treat the file as the single source of truth for that page and read the page first (`get`) if you're not sure what's there. Sub-pages and child databases nested under the page are never touched — they're skipped rather than archived, and a warning on stderr names each one that was kept.

**Supported Markdown.** Headings (`#`/`##`/`###`, deeper levels flatten to h3), paragraphs, bulleted and numbered lists, task checkboxes (`- [ ]` / `- [x]`), fenced and indented code blocks, blockquotes, `---` dividers, and inline **bold**, *italic*, `code`, ~~strikethrough~~, and links. List and quote nesting is supported to 2 levels. Tables, images, raw HTML, and nesting past 2 levels aren't dropped — each **degrades** to the closest supported block (a table becomes a plain-text code block, an image becomes a link, deeper nesting is promoted up a level) and prints a warning to stderr naming what happened, so nothing silently disappears but nothing blocks the write either. A file over 1 MiB is rejected before any request is made (exit code 2).

**Cost.** There's no bulk-delete endpoint in Notion's API, so replacing a body is `O(n)` in the number of blocks already on the page: append the new content, then delete the old blocks one by one. A page with a lot of existing content takes correspondingly longer, and `notion-track` prints progress lines to stderr (blocks appended, blocks deleted so far) so a long run doesn't look hung.

**Concurrency.** Two `--body-file` runs against the same page racing each other can both append before either deletes, leaving the body duplicated — there's no lock to take on a Notion page. Don't run concurrent body writes against one page.

With `--json`, a successful write adds a `body` object: `{"blocks_written": N, "blocks_deleted": N}`. If the properties write succeeds but the body replace fails partway, the command still exits 1 (not 0), and `--json` prints `body: {"written": false, "error": "...", "blocks_written": N, "blocks_deleted": N}` — `written` tells you the body is *not* in the state the file describes, while `page` in the same output still reflects whatever properties were applied, since those are two separate Notion API calls and the first can succeed even if the second doesn't.

### `get` — read one row

```
notion-track get (--ticket <key> | --page-id <id>) [--json]
```

Prints the row's ticket, title, status and URL. `--ticket` and `--page-id` are mutually exclusive and exactly one is required — see `set` above for what `--page-id` accepts and how it's validated. Fails with exit code 3 if not found (Notion's 404 doesn't distinguish "no such page" from "never shared with this integration" — the error message says so), 4 if a ticket key matches more than one row (see [Limitations](#limitations)), or 2 for a malformed page id or one outside the active profile's data source. Unlike `set`, `get --page-id` accepts a page whose parent carries no data source at all — a read cannot do any harm with an unconfirmed page the way a write could.

### `list` — read many rows

```
notion-track list [--status <status>] [--json]
```

Lists every row, or only those matching `--status`. An unknown status value fails fast with exit code 2, naming the values Notion actually allows for that property.

When nothing matches, the human-readable form prints `no matching tasks` **to stderr** and exits 0 — stdout stays empty, so `list | wc -l` counts rows and nothing else. `list --json` prints `[]` and says nothing on stderr.

### `doctor` — check the setup

```
notion-track doctor [--json]
```

Runs four checks — `token`, `data_source`, `properties`, `duplicates` — and prints each as `ok`, `warn`, or `fail` with an actionable detail message. A `warn` (e.g. the status property's type changed since `init` ran) does not fail the command; any `fail` makes it exit non-zero. `properties` and `duplicates` still run even when `data_source` fails, so a broken setup gets diagnosed in one pass instead of one symptom at a time.

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
      ticket: Ticket   # rich_text or title property holding the ticket key
      status: Status   # status or select property
      title: Name      # title property
      due: Due         # optional: date property
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

Precedence:

- **Profile selection:** `--profile` flag → `NOTION_TRACK_PROFILE` → `default_profile` in the config file.
- **`database_id` / `data_source_id`:** the env vars above always override whatever the resolved profile has on file, regardless of how that profile was chosen — this is what lets a CI job point an existing profile at a different data source without touching the committed file.
- **Token:** `NOTION_TOKEN` → `credentials.yml`. A token read from the environment is never written back to `credentials.yml` — a CI secret can never leak onto disk through a normal run. Run `notion-track doctor` if you need to see which source actually won.
- **Config file location:** `--config` flag → the OS default path above. There is no environment variable for the path itself, and no equivalent flag for `credentials.yml`.

## JSON output

Every `--json` shape below is a **documented, stable scripting contract**: a key is never renamed or removed without a breaking-change announcement.

A row (`get --json`, and each entry of `list --json`):

```json
{
  "ticket": "BDF-231",
  "title": "Hardening",
  "status": "In progress",
  "page_id": "1a2b3c4d-...",
  "url": "https://www.notion.so/...",
  "last_edited_time": "2026-07-23T10:15:00Z"
}
```

If the configured property mapping names a column the row doesn't actually carry, the corresponding field comes back as an empty string rather than an error — a broken mapping is `doctor`'s job to report, not a reason to fail every read.

`upsert --json` / `set --json`:

```json
{
  "action": "created",
  "page": { "ticket": "BDF-231", "title": "Hardening", "status": "In progress", "page_id": "...", "url": "...", "last_edited_time": "..." }
}
```

`action` is `"created"` or `"updated"`.

`doctor --json` — an array of checks, one per `token` / `data_source` / `properties` / `duplicates`:

```json
[
  { "name": "token", "status": "ok", "detail": "token from environment\n  authenticated as notion-track" },
  { "name": "data_source", "status": "ok", "detail": "reachable: Tasks" },
  { "name": "properties", "status": "ok", "detail": "all mapped properties exist with the expected types" },
  { "name": "duplicates", "status": "ok", "detail": "42 rows, no repeated ticket keys" }
]
```

`status` is `"ok"`, `"warn"`, or `"fail"`; `detail` is omitted only when empty, which does not happen in practice.

## Exit codes

Pipelines can branch on these without parsing any message text:

| Code | Name | Meaning |
|---|---|---|
| `0` | OK | success |
| `1` | Error | a generic failure — a network/API error, or `doctor` reporting a failed check other than `token` |
| `2` | Usage | the invocation cannot work as written: a missing/invalid flag, `--ticket` and `--page-id` both given or neither given, an unknown command, no config yet (`notion-track init` was never run), a status value the data source doesn't allow, a malformed `--page-id`, or a `--page-id` that resolves outside the active profile's data source |
| `3` | Not found | the requested ticket has no matching row, or the page id has no matching page (or one not shared with this integration) (`get`, `set`) |
| `4` | Duplicate | the ticket key matches more than one row (`upsert`, `set`, `get`) |
| `5` | Auth | no token was found (including a `credentials.yml` that exists but can't be read), or Notion rejected it (401/403) — including `doctor`, when its `token` check is the only one that failed |

## CI usage

Because the config file has no secret in it, the common pattern is to **commit it to the repository** and point at it explicitly with `--config`, while the token comes from a CI secret:

```yaml
# .github/workflows/notion.yml
- name: Install notion-track
  run: go install github.com/marcoarnulfo/notion-cli/cmd/notion-track@latest

- name: Mark the ticket done
  run: notion-track upsert --ticket "$TICKET" --status "Done" --config notion-track.yml
  env:
    NOTION_TOKEN: ${{ secrets.NOTION_TOKEN }}
    TICKET: ${{ github.event.inputs.ticket }}
```

A thin composite GitHub Action wrapping this is on the [roadmap](#roadmap); today, `go install` + the two lines above is the whole integration.

## Limitations

These are current, deliberate tradeoffs — not bugs to be surprised by:

1. **Every change is attributed to the integration, not to you.** Notion records edits made through the API as made by the integration's bot identity. If you check a page's edit history in Notion, you will see the integration's name, never the human or CI job that ran the command.
2. **`upsert` and `get` fail on duplicate ticket keys instead of picking one.** If more than one row shares the same ticket key, `notion-track` refuses to guess which one you meant — it exits with code 4 and lists the offending rows. Run `notion-track doctor` to find and clean them up.
3. **Two concurrent jobs creating the same new ticket can race into a duplicate.** `upsert`'s create-or-update decision reads the current rows, then writes; the Notion API offers no unique-constraint or compare-and-swap primitive to close that window. This is not preventable client-side — `doctor`'s duplicate scan is the mitigation, not a fix.
4. **Only a Workspace Owner can set this up.** Creating the integration and sharing a database with it both require Workspace Owner permissions in Notion. A workspace guest — one of the reasons this tool exists in the first place — cannot do either step, but can use the tool freely once someone with Owner rights has.
5. **No interactive TUI yet.** There is no wizard or browsing UI; every command here is flag-driven. Tracked in the [Roadmap](#roadmap).
6. **`--body-file` replaces the whole page body, with no lock and no undo.** It owns the body: each run overwrites everything there, hand-edited content included, and two runs racing the same page can duplicate it. See `--body-file` under [Usage](#usage) above.

## Use it from an AI agent

Because the tool is quiet on success, speaks `--json` with a stable schema, and returns [differentiated exit codes](#exit-codes), an agent can drive it as reliably as a script does — no scraping of human output. A ready-made [Claude Code](https://claude.com/claude-code) skill lives in **[`skills/notion-track/`](skills/notion-track/)**: it teaches an agent which command to reach for and how to stay safe (read before writing, never invent a status, branch on exit codes). Install it by copying its `SKILL.md` into `~/.claude/skills/notion-track/`, then ask your agent to "mark that task done on Notion". A `notion-track mcp` server is on the [roadmap](#roadmap) for hosts that speak MCP rather than the shell.

## Contributing

Contributions are welcome — this is a free, open-source project. See **[CONTRIBUTING.md](CONTRIBUTING.md)** for the development setup, the checks to run before opening a PR, and the project's non-negotiable architectural rules. Please also read the [Code of Conduct](CODE_OF_CONDUCT.md). Found a security issue? See [SECURITY.md](SECURITY.md) instead of opening a public issue.

## Roadmap

Implemented today: `init` (flag-driven, with `--list`), `upsert`, `set`, `get`, `list`, `doctor`; `--body-file` on `upsert`/`set` to write the page body from Markdown; `--json` on every command that produces output; profiles; retry with backoff.

Not yet built:

- **Interactive `init` wizard** — a guided TUI alternative to today's flag-only form.
- **Browsing TUI** — an interactive view over the tracked rows.
- **Prebuilt binaries** — a GoReleaser pipeline publishing GitHub Releases for macOS/Linux/Windows; today, `go install` or building from source are the only options.
- **A composite GitHub Action** wrapping the binary, so a workflow step doesn't need its own `go install`.
- **An MCP server adapter** over the same `internal/service` layer the CLI uses today.

## License

[MIT](LICENSE)
