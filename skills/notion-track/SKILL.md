---
name: notion-track
description: >-
  Manage Notion task-tracking rows from the terminal with the notion-track CLI:
  create tasks, change their status (mark done / in progress / archived), read a
  single task, and list tasks with a status filter. Use whenever the user wants
  to touch a task on their Notion board — create one, move it to another status,
  look one up, or list what's in a given state. Triggers on: "task su Notion",
  "segna come fatto", "mettilo in corso", "aggiorna lo stato", "crea un task",
  "elenca i task", "che task ho da fare", "mark done", "update the status",
  "notion-track". The user often phrases these in Italian.
---

# notion-track — managing Notion tasks from the CLI

`notion-track` is an installed command-line tool that syncs a single Notion
task-tracking database. Use it — via the shell — instead of asking the user to
open Notion, whenever they want to create, update, read, or list tasks.

The tool is already authenticated (the token lives in a local credentials file);
you never handle the token. Just run the commands.

## The one rule that prevents mistakes

**Read before you write.** A write command acts on whichever row it resolves,
and there is no undo. Before a `set` or an `upsert` that updates an existing
task, confirm you're aiming at the right row — with `get`, or by having just
listed it. When the user's request is ambiguous about *which* task, ask rather
than guess.

Everything a machine reads should come from `--json`, never from parsing the
human-readable lines.

## How a task is identified

A task is addressed in one of two ways. Pick deliberately:

- **By ticket key** (`--ticket "<value>"`) — the tool looks up the row whose
  key column equals that value. In this workspace the key column *is the task
  title* (see "This workspace" below), so `--ticket` is the exact task name.
  Renaming a task in Notion changes its key, so a name that was valid yesterday
  may not resolve today.
- **By Notion page id** (`--page-id <id>`) — addresses one specific row
  directly, no lookup. The id is stable forever, even if the task is renamed.
  It accepts the page URL copied from Notion ("Copy link"), a bare 32-hex id, or
  a dashed UUID. Use this when the user pastes a Notion link or id, or when the
  task might have been renamed.

`--ticket` and `--page-id` are mutually exclusive on `get` and `set`; exactly
one is required. `upsert` only takes `--ticket` (see below for why).

## Commands

### Create or update a task by name — `upsert`

```sh
notion-track upsert --ticket "<name>" [--status "<status>"] [--title "<title>"] [--due YYYY-MM-DD] [--json]
```

Creates the row if no task has that key, updates it if one does. Running it
twice yields one row — safe to repeat. `--ticket` is required; the others are
set only if given. `upsert` cannot address a task by `--page-id`: at creation
time the page doesn't exist yet, so there's no id to use.

`--json` returns `{"action":"created"|"updated","page":{...}}` — read `action`
to tell which happened, and `page.page_id` to capture the id for later.

### Change an existing task — `set`

```sh
notion-track set --ticket "<name>"     --status "<status>" [--title ...] [--due ...] [--json]
notion-track set --page-id <id-or-url> --status "<status>" [--title ...] [--due ...] [--json]
```

Updates only. **Fails if the task doesn't exist** (exit 3) instead of creating
it — that's the point of `set` versus `upsert`. Only the flags you pass are
touched; everything else on the row is left alone. Prefer `set` over `upsert`
when the task is meant to already exist, so a typo surfaces as an error instead
of a stray new row.

### Read one task — `get`

```sh
notion-track get --ticket "<name>"     [--json]
notion-track get --page-id <id-or-url> [--json]
```

Use it to confirm a task exists and to see its current state before changing it.
With `--json` the fields are `ticket`, `title`, `status`, `page_id`, `url`,
`last_edited_time` — a stable schema, safe to parse.

### List tasks — `list`

```sh
notion-track list [--status "<status>"] [--json]
```

All rows, or only those in one status. `--json` returns an **array** (`[]` when
empty, never `null`), each element with the same fields as `get`. This is the
way to answer "what do I have in progress?" or to find a task's page id.

### Diagnose — `doctor`

```sh
notion-track doctor [--json]
```

Run this first if any command errors in a way you don't understand. It checks
the token, database access, the property mapping, and duplicate keys, and prints
what's wrong and how to fix it.

## Exit codes — branch on these, don't parse messages

| Code | Meaning | What to do |
|---|---|---|
| 0 | success | proceed |
| 2 | bad usage (missing/invalid flag, unknown status, malformed page id) | fix the invocation; a rejected status means the value isn't one the board allows |
| 3 | task not found | with `set`/`get`: the ticket or page id doesn't match a row — don't retry as `upsert` without checking with the user |
| 4 | duplicate key | more than one row has that ticket key; the tool refuses to guess. Surface it and run `doctor` to list the duplicates |
| 5 | auth failure | the token is missing or invalid; tell the user to run `notion-track init` |
| 1 | other error | report it |

A rejected status (exit 2) is common and recoverable: the board accepts only a
fixed set of status values. Never invent one — use a value the board already
has (list them with `doctor` or by looking at existing tasks).

## Safe patterns

Change a task's status, checking it exists first:

```sh
notion-track get --ticket "Deploy staging" --json   # confirm it's there and see its state
notion-track set --ticket "Deploy staging" --status "Fatto"
```

Update a task the user pasted a Notion link for (name unknown, immune to rename):

```sh
notion-track set --page-id "https://www.notion.so/Deploy-23fb4e5c8a5f4d21b7c9d0e1f2a3b4c5" --status "Fatto"
```

Create a task and keep its id for later steps:

```sh
PID=$(notion-track upsert --ticket "Backup NAS" --status "Da fare" --json | jq -r .page.page_id)
notion-track set --page-id "$PID" --status "Fatto"
```

Answer "what am I working on?":

```sh
notion-track list --status "In corso" --json
```

## This workspace (current, verify with `doctor` if in doubt)

The values below describe the user's board *as configured now*. If a command
fails unexpectedly, the setup may have changed — run `notion-track doctor` and
`notion-track list --json` to rediscover it rather than trusting this section
blindly.

- **Board**: "Panoramica Task".
- **Key column = title**: the ticket key and the task title are the same column
  ("Nome task"). So `--ticket "X"` means the task literally named X, and creating
  with `upsert --ticket "X"` sets its name to X. Because the key is the name,
  prefer `--page-id` for any task that might get renamed.
- **Status values**: `Da fare`, `In corso`, `In revisione`, `Fatto`,
  `Archiviato`. These are the only accepted `--status` values; anything else is
  rejected with exit 2.
- **Other settable fields**: `--due YYYY-MM-DD` (the "Deadline" date). Columns
  like Referente, Urgenza and URL exist on the board but are not managed by this
  tool.
- **Attribution caveat**: every change is recorded by the integration bot
  ("BDF Automation"), not by the person running the command. If the user asks
  "who moved this card", that information isn't captured.

## When NOT to reach for this skill

- Anything beyond task rows — page bodies, comments, arbitrary Notion pages,
  other databases — is out of scope; `notion-track` only touches this one board.
- Bulk changes across many tasks: the tool has no batch command yet. Loop over
  individual `set` calls only with the user's explicit go-ahead, and stop on the
  first non-zero exit rather than plowing through.
