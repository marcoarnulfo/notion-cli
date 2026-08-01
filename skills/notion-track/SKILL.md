---
name: notion-track
description: >-
  Use when the user wants to touch a task on their Notion board — create one,
  change its status, assign it, set its priority, look one up, list what's in a
  given state, or apply many changes at once. Often phrased in Italian ("segna
  come fatto", "mettilo in corso", "crea un task", "che task ho da fare",
  "assegna a Sam", "prendi in carico", "è urgente", "cosa faccio prima") or in
  English ("mark done", "what's on my plate", "who owns X", "what's urgent").
  Drives the installed notion-track CLI.
---

# notion-track — managing Notion tasks from the CLI

`notion-track` syncs one Notion task-tracking database. Use it from the shell
instead of asking the user to open Notion. It is already authenticated — the
token lives in a local credentials file and you never handle it.

If the command isn't found, or the user asks to update it:
`go install github.com/marcoarnulfo/notion-cli/cmd/notion-track@latest`
(installs and upgrades both; needs Go, puts the binary in `$(go env GOPATH)/bin`).
`notion-track --version` reports what is installed — check it first when a
command misbehaves and a stale build is a plausible reason.

**This file is a hand-installed copy and does not update with the tool.** Two
signs it has fallen behind the binary: a command rejects a flag documented
here, or `notion-track <command> --help` lists one this file never mentions.
Either way the copy is older than the tool — say so instead of working around
it, and offer the refresh:

```sh
curl -fsSL https://raw.githubusercontent.com/marcoarnulfo/notion-cli/main/skills/notion-track/SKILL.md \
  -o ~/.claude/skills/notion-track/SKILL.md
```

The mismatch runs the other way too: a flag described here that the installed
binary doesn't have means the *tool* is behind, and `go install …@latest`
above is the fix.

## Before you write anything

**1. Start the session by looking.** Board setup differs per workspace and
nothing below is safe to assume:

```sh
notion-track doctor          # token, database, property mapping, duplicates
notion-track list --json     # real rows, and the status values actually in use
```

**2. Read before you write.** A write acts on whichever row it resolves, and
there is no undo. Before a `set` or an `upsert` that updates an existing task,
confirm you're aiming at the right row — with `get`, or by having just listed
it. When the request is ambiguous about *which* task, ask rather than guess.

**3. Never run two writes at once.** No shell backgrounding, no parallel tool
calls. There is no locking anywhere in this tool: two `upsert`s racing on the
same ticket create a duplicate row, two body writes on one page duplicate the
body, and nothing detects a change made between your `get` and your `set` — it
is silently overwritten. `apply` serializes for you; prefer it to a loop.

**4. Preview with `--dry-run`** (on `upsert`, `set` and `apply`): it reports
what the write *would* do and writes nothing, exiting 0. With `--json` the
output is `{"dry_run":true,"plan":{...}}`. Its limits are real, see below.

**5. Parse `--json`, never the human-readable lines.**

### What `--dry-run` does not catch

It re-runs the same lookups and validates status, assignee and priority against
the board — so a value the board rejects fails on the dry run. But:

- **`--due` is not validated at all.** The date string is passed through to
  Notion untouched, so `--due "domani"` passes the dry run and fails the real
  write. Send `YYYY-MM-DD` and nothing else.
- **The body is not sent to Notion.** A `--body-file` problem can still surface
  on the real write, *after* the properties have been written.
- **In `apply`, each entry is checked against the board as it is now**, not
  against what earlier entries would have created. A `set` that depends on an
  `upsert` higher in the same manifest fails the dry run with exit 3. That is an
  expected false alarm — **do not "fix" it by turning that `set` into an
  `upsert`**, which would remove the typo protection that made it a `set`.
- Its answer ages. If time has passed, run it again.

## Know this board

Five things change how you address and create tasks. Discover them; the skill
cannot tell you.

| What to establish | How |
|---|---|
| **Is the ticket key its own column, or the title?** | `doctor`'s mapping. If the key *is* the title, `--ticket "X"` means the task literally named X, creating with `upsert --ticket "X"` names it X, and a rename breaks lookup by name — `--page-id` is then the stable handle |
| **Which status values does the board accept?** | No command prints them. `list --json` shows the ones rows carry; the exit-2 rejection names *every* allowed value, so one wrong guess costs one failed call — cheapest with `--dry-run`, which writes nothing |
| **Is there an assignee column, and does `me` resolve?** | When mapped, `doctor` runs an `assignee` check (absent otherwise) saying whether `me` resolves. When unmapped, `doctor` stays silent and `--assignee` failing with exit 1 is the only signal |
| **Is there a priority column?** | Same silence: `--priority` failing with exit 1 is the only signal. `doctor` never lists its values either — read them off `list` |
| **Is there a board id column?** | `list --json` / `get --json` show a non-empty `id` only when mapped. `--id` on an unmapped board fails with **exit 2, not 1** — the one role that differs |

Two things that hold on every board: every change is recorded under the
integration's bot identity, so "who moved this card" is not recoverable; and
running `notion-track` with no arguments at a terminal opens a full-screen
browsing TUI, so always use an explicit subcommand.

With several profiles configured, `--profile <name>` (and `--config <path>`)
select which board you are driving. Addressing *and* the `me` identity are
per-profile: the wrong profile assigns the right name on the wrong board.

## Addressing a row

`--ticket`, `--id` and `--page-id` are mutually exclusive on `get` and `set`,
and exactly one is required. `upsert` takes only `--ticket` — at creation time
there is no page to point at.

- **`--ticket "<value>"`** — the row whose key column equals that value. Exact,
  not fuzzy. A trailing space is a different key, and on `upsert` a key that
  matches nothing is **created**, not reported.
- **`--id <board-id>`** — the short id Notion shows on the row (`TASK-271`, or
  the bare `271`), the one a person reads aloud. Only when the board maps an id
  column.
- **`--page-id <id-or-url>`** — one specific row, no lookup: a "Copy link" URL,
  a bare 32-hex id, or a dashed UUID. Stable across renames. A page that exists
  but belongs to another data source fails with **exit 2** (wrong id, or wrong
  `--profile`) — not exit 3.

**To rename a task on a board where the key is the title**, the only correct
form is `set --page-id <id> --title "New name"`. `set --ticket Old --title New`
exits 0 having changed nothing (the ticket value wins over a separate title on
that shared column), and `upsert --ticket New` creates a second row.

## Commands

### `upsert` — create or update by name

```sh
notion-track upsert --ticket "<name>" [--status "<status>"] [--title "<title>"] [--due YYYY-MM-DD] [--assignee "<name-or-me>"] [--priority "<value>"] [--body-file <path>] [--dry-run] [--json]
```

Creates the row if no task has that key, updates it if one does; running it
twice yields one row. `--json` returns `{"action":"created"|"updated","page":{...}}`
— read `action` to tell which happened and `page.page_id` to capture the id.

Prefer `set` whenever the task is meant to already exist, so a typo surfaces as
an error instead of a stray new row.

### `set` — update only

```sh
notion-track set --ticket "<name>"     --status "<status>" [--title ...] [--due ...] [--assignee "<name-or-me>"] [--priority "<value>"] [--body-file <path>] [--dry-run] [--json]
notion-track set --id <board-id>       --status "<status>" [...]
notion-track set --page-id <id-or-url> --status "<status>" [...]
```

**Fails with exit 3 if the task doesn't exist** instead of creating it — that is
the whole point of `set` versus `upsert`. Only the flags you pass are touched.

### `--assignee` / `--unassign` (on `upsert`, `set`; `--assignee` also on `list`)

```sh
notion-track set --ticket "<name>" --assignee "Sam Rivera"
notion-track set --ticket "<name>" --assignee sam    # a partial name is enough when unambiguous
notion-track set --ticket "<name>" --assignee me     # "prendi in carico"
notion-track set --ticket "<name>" --unassign        # clears it
```

A name resolves against what the board's column offers: exact, then
case-insensitive, then case-insensitive substring. **Don't invent a full name** —
pass what the user said; an ambiguous or unknown value fails with the real
options listed, which beats guessing. But a *unique* partial match is applied
silently, so **report the resolved name back to the user** (it's in the
response's `assignee` field): "sam" can match the only Sam on the board and
still be the wrong person.

`--assignee ""` is a usage error, not a way to clear — use `--unassign`. The two
are mutually exclusive.

`me` is the configured identity, resolved in this order: `NOTION_TRACK_ME` →
the profile's entry in `credentials.yml` → the profile's legacy `me:` in
`config.yml`. With nothing configured it fails (exit 2): tell the user to run
`notion-track init --me "<name>"` — don't substitute a guessed name and don't
tell them to export a variable. That command writes only the identity for the
profile in use, and needs a board that maps an assignee column.

Exit 5 from `--assignee me` has two causes and the message tells them apart: an
unreadable credentials file (say so — the fix is repairing the file) versus the
ordinary missing-token failure every command gives.

### `--priority` (on `upsert`, `set`; also on `list` to filter)

```sh
notion-track set --ticket "<name>" --priority alta      # partial values resolve like names do
notion-track list --priority ALTA --assignee me --json  # what's urgent and mine
```

Resolves exactly like `--assignee`, with the same "don't invent a value" rule
and the same empty-string usage error. **Never assume a vocabulary** like
ALTA/MEDIA/NORMALE — read the board's real values.

Unlike assignee, priority has **no way to clear a value and no `me`**: there is
no `--unpriority` and no `list --unprioritized`. Asked to "togli la priorità",
say it has to be done in Notion rather than reaching for a flag.

### `--body-file` — writing the page body (on `upsert`, `set`)

A Markdown file (`-` for stdin) that becomes the page body.

**It owns the body: every run makes the body exactly equal to the file**,
deleting whatever was there, hand-written content included.

**You cannot see what you are about to delete** — `get` returns properties
only, and no command in this tool reads a page body. So: use `--body-file`
freely on pages this workflow created, and on any pre-existing row **ask the
user before overwriting**. "I ran `get` first" is not a check on the body.

Sub-pages and child databases are preserved **only at the top level of the
body**. One nested inside a toggle, a column or a callout is deleted along with
the block that contains it.

`--json` adds `body:{blocks_written,blocks_deleted}` on success. On partial
failure (properties written, body failed) the command exits 1 and `body` gains
`written:false` plus an `error`, while `page` still reflects the properties that
did land. So: treat `written:false` as failure — the key is absent on success.

Add **`--expand`** to substitute `{{ticket}}` and `{{date}}` (today, as
`YYYY-MM-DD`) before sending. Without it, braces are left alone, so a body that
legitimately contains `{{...}}` is safe by default. An unknown placeholder is an
error naming the line — never invent placeholder names. Note that with
`--page-id` or `--id` addressing there is no ticket in the invocation, so
`{{ticket}}` expands to an empty string.

### `get` — read one task

```sh
notion-track get --ticket "<name>" [--json]     # or --id / --page-id
```

`--json` fields, a stable schema safe to parse: `id`, `ticket`, `title`,
`status`, `page_id`, `url`, `last_edited_time`, `assignee`, `priority`. The
keys are always present; `id`, `assignee` and `priority` are empty **both**
when the row carries no value and when the board doesn't map the role, so check
for an empty string rather than a missing key.

### `list` — many tasks

```sh
notion-track list [--status "<status>"] [--assignee "<name-or-me>"] [--unassigned] [--priority "<value>"] [--json]
```

`--json` returns an **array** (`[]` when empty, never null) of the same fields
as `get`. This answers "cosa devo fare io" (`--assignee me --status ...`), "chi
ha in mano X" (`--assignee "<name>"`), "task senza referente" (`--unassigned`,
mutually exclusive with `--assignee`) and "cosa c'è di urgente" (`--priority`).

### `apply` — many changes at once

```sh
notion-track apply --file <manifest.json|manifest.csv> [--dry-run] [--expand] [--json]
```

**Use this instead of looping over `set`/`upsert`** — one entry per write,
applied in order, in one process. A loop is only worth it when an entry's flags
depend on a previous entry's output.

```json
[
  {"op": "set", "ticket": "TASK-1", "status": "In corso", "assignee": "sam", "priority": "alta"},
  {"op": "set", "ticket": "TASK-2", "status": "Fatto", "unassign": true}
]
```

Fields: `op` (`upsert` or `set`), `ticket` (required), `title`, `status`,
`due`, `body_file`, `assignee`, `unassign`, `priority`. Unknown fields are
errors, so spell them exactly. `body_file` is resolved relative to the
manifest. Format comes from the extension; a CSV needs a header row.

**Write `"op":"set"` on every entry that updates an existing task.** `op`
defaults to `upsert`, so an entry with a typo — or a stray trailing space, which
is not trimmed in JSON manifests — silently creates a ghost row with a status
already set instead of failing. In a dry run of an update-only manifest, **every
`created` in the plan is a typo**.

Build the manifest in a file, dry-run it when you wrote it yourself, and show
the user the plan before applying for real.

**It stops at the first failing entry**, reporting how many were applied and
exiting with that entry's code. With `--json`:
`{"applied":N,"total":M,"entries":[...]}`, each entry carrying its `action` or
its `error`. Note that exit 2 from `apply` means *either* a manifest rejected
before anything ran *or* an entry that failed validation mid-run (an unknown
status, an ambiguous assignee, or `assignee` and `unassign` on one entry — that
conflict is caught at write time, not at parse time). Read `applied` before
concluding that nothing was written.

### `doctor` — diagnose

```sh
notion-track doctor [--json]
```

Run it first when a command fails in a way you don't understand. It checks the
token, database access, the property mapping, duplicate keys, and whether a
git-tracked file looks like it carries the user's token. Only a `fail` exits
non-zero; a `warn` is worth reporting but blocks nothing.

It does **not** list the accepted values of any select column — not statuses,
not priorities. Nothing does.

If its `secrets` check flags a file, **do not open or print that file**: relay
the warning as it stands, token rotation included.

## Exit codes — branch on these, don't parse messages

| Code | Meaning | What to do |
|---|---|---|
| 0 | success | proceed |
| 1 | other error, including an `--assignee`, `--priority` or `--due` role not mapped on this board | report it; for an unmapped role say so and point at `init` rather than retrying (unmapped **id** is the exception — it's exit 2) |
| 2 | bad usage: missing or invalid flag, unknown status, malformed `--page-id`/`--id`, an unknown or ambiguous `--assignee`/`--priority`, an empty value for either, `--assignee` with `--unassign`, `--assignee me` with no identity, `--id` on a board with no id column, or a `--page-id` belonging to another data source | fix the invocation; a rejected value means it isn't one the board allows — read the options the error lists rather than guessing again |
| 3 | task not found | the ticket, board id or page id matches no row — don't retry as `upsert` without asking the user |
| 4 | duplicate key | more than one row has that key and the tool refuses to guess; surface it and run `doctor` to list them |
| 5 | auth failure | token missing or invalid — tell the user to run `notion-track init`. Also what `--assignee me` gives when the credentials file can't be read, where the fix is that file |

## When a write fails halfway

- **Body failed after properties** (exit 1, `body.written:false`): the page may
  hold old and new blocks together — the new body is appended before the old is
  deleted. Re-run the same command; it converges.
- **`apply` stopped mid-run**: fix the entry it named and re-run the whole
  manifest — entries are idempotent — or resume from the reported index (1-based).
  The exception is `--expand` with `{{date}}`, which changes across midnight.
- **"write outcome unknown; re-run to converge"**: a timeout or 5xx left the
  result unknown. Re-run the same command once, then confirm with `get`.

## Over MCP instead of the shell

If your host speaks MCP, `notion-track mcp` serves four tools over stdio:
`upsert_task`, `set_task`, `get_task`, `list_tasks`. Same code underneath, so
read-before-write, never-invent-a-status and branch-on-the-outcome all apply.
Three differences matter:

- **Envelopes differ from the CLI's.** A write returns `{"action":…,"row":{…}}`
  — the key is `row`, not `page`. `list_tasks` wraps its array in a `rows` key
  (`{"rows":[…]}`) rather than returning a bare array. Only `get_task` matches
  `get --json` exactly. The fields inside a row are the same either way.
- **Rows are addressed only by `ticket`.** There is no MCP equivalent of
  `--id` or `--page-id`; passing one is rejected or ignored as an unknown field
  (a result still *carries* `id`, it just can't take one).
- **There is no preview and no body.** No `dry_run` argument, no body writing,
  no `apply` or `doctor` equivalent — an MCP write always writes. Compensate
  with `get_task` immediately before `set_task`, and prefer `set_task` over
  `upsert_task` for anything that should already exist.

## Out of scope

- Comments, arbitrary Notion pages and other databases: `notion-track` only
  touches this one board. Page bodies are writable, with the replace semantics
  above.
- The assignee is a `select` value, not Notion's "person" type: no teams, no
  multi-assignment, no lookup against workspace members. Don't try to resolve a
  Notion user id or invent a name the column hasn't offered.
- Priority is a `select` too — the board's fixed vocabulary, not a scale, and
  not clearable through this tool.
