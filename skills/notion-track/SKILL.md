---
name: notion-track
description: >-
  Manage Notion task-tracking rows from the terminal with the notion-track CLI:
  create tasks, change their status (mark done / in progress / archived), assign
  or clear who owns one, mark how urgent one is, read a single task, and list
  tasks filtered by status, by assignee, or by priority. Use whenever the user
  wants to touch a task on their Notion board — create one, move it to another
  status, assign or reassign it, mark it urgent, look one up, list what's in a
  given state, assigned to someone, or at a given priority, or apply many
  changes at once from a file. Triggers on: "task su Notion", "segna come
  fatto", "mettilo in corso", "aggiorna lo stato", "crea un task", "elenca i
  task", "che task ho da fare", "creali tutti", "aggiornali tutti", "assegna a
  Sam", "prendi in carico", "chi ha in mano X", "cosa devo fare io", "task
  senza referente", "è urgente", "priorità alta", "mettilo in alta", "cosa c'è
  di urgente", "cosa faccio prima", "mark done", "update the status", "assign
  this to X", "take ownership of X", "who owns X", "what's on my plate",
  "unassigned tasks", "it's urgent", "high priority", "mark it high priority",
  "what's urgent", "what should I do first", "notion-track". The user often
  phrases these in Italian.
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

Every write command takes **`--dry-run`**, which reports whether it would
create or update, which row, and which columns — and writes nothing. Use it
whenever you are not certain a write will land where you intend, and always
before applying a manifest you built rather than one the user wrote.

Everything a machine reads should come from `--json`, never from parsing the
human-readable lines.

## How a task is identified

A task is addressed in one of three ways. Pick deliberately:

- **By ticket key** (`--ticket "<value>"`) — the tool looks up the row whose
  key column equals that value. In this workspace the key column *is the task
  title* (see "This workspace" below), so `--ticket` is the exact task name.
  Renaming a task in Notion changes its key, so a name that was valid yesterday
  may not resolve today.
- **By board id** (`--id <board-id>`) — addresses a row by the short id
  Notion itself assigns and shows on the row (`TASK-271`, or the bare number
  `271` on its own) — the one a person reads aloud. Use it when the user gives
  you that id instead of a name. Only works if this board maps an id column;
  not every board does (see "This workspace" below) — `list --json` or
  `get --json` show a non-empty `id` key only when the role is mapped.
- **By Notion page id** (`--page-id <id>`) — addresses one specific row
  directly, no lookup. The id is stable forever, even if the task is renamed.
  It accepts the page URL copied from Notion ("Copy link"), a bare 32-hex id, or
  a dashed UUID. Use this when the user pastes a Notion link or id, or when the
  task might have been renamed.

`--ticket`, `--id` and `--page-id` are mutually exclusive on `get` and `set`;
exactly one is required. `upsert` only takes `--ticket` (see below for why).

## Commands

### Create or update a task by name — `upsert`

```sh
notion-track upsert --ticket "<name>" [--status "<status>"] [--title "<title>"] [--due YYYY-MM-DD] [--assignee "<name-or-me>"] [--priority "<value>"] [--body-file <path>] [--dry-run] [--json]
```

Creates the row if no task has that key, updates it if one does. Running it
twice yields one row — safe to repeat. `--ticket` is required; the others are
set only if given. `upsert` cannot address a task by `--page-id`: at creation
time the page doesn't exist yet, so there's no id to use.

`--json` returns `{"action":"created"|"updated","page":{...}}` — read `action`
to tell which happened, and `page.page_id` to capture the id for later. On a
body write, success adds `body:{blocks_written,blocks_deleted}`. On partial
failure (properties written, body failed) the command exits 1 and `--json`
adds `body:{written:false,error,...}` while `page` still reflects the
properties that did get applied — check `body.written` before assuming a
`--body-file` call fully succeeded.

### Change an existing task — `set`

```sh
notion-track set --ticket "<name>"     --status "<status>" [--title ...] [--due ...] [--assignee "<name-or-me>"] [--priority "<value>"] [--body-file <path>] [--dry-run] [--json]
notion-track set --id <board-id>       --status "<status>" [--title ...] [--due ...] [--assignee "<name-or-me>"] [--priority "<value>"] [--body-file <path>] [--dry-run] [--json]
notion-track set --page-id <id-or-url> --status "<status>" [--title ...] [--due ...] [--assignee "<name-or-me>"] [--priority "<value>"] [--body-file <path>] [--dry-run] [--json]
```

Updates only. **Fails if the task doesn't exist** (exit 3) instead of creating
it — that's the point of `set` versus `upsert`. Only the flags you pass are
touched; everything else on the row is left alone. Prefer `set` over `upsert`
when the task is meant to already exist, so a typo surfaces as an error instead
of a stray new row.

### Assign a task, or clear who owns it — `--assignee` / `--unassign`

Available on `upsert` and `set`. This is what "assegna a Sam", "prendi in
carico questo task", or "assign this to X" mean in practice:

```sh
notion-track set --ticket "<name>" --assignee "Sam Rivera"
notion-track set --ticket "<name>" --assignee sam     # a partial name is enough when it's unambiguous
notion-track set --ticket "<name>" --assignee me       # "prendi in carico" / "assign it to me"
notion-track set --ticket "<name>" --unassign           # clears it — "task senza referente" going forward
```

`--assignee` takes a name and resolves it against whatever the board's assignee
column actually offers — exact match, then case-insensitive, then a
case-insensitive substring — so `sam` is enough when only one option
contains it. **Don't invent or guess a full name**: pass what the user said and
let resolution do the matching; if it's genuinely ambiguous or unknown the
error message lists the real options, which is more reliable than guessing
yourself. `--assignee` and `--unassign` are mutually exclusive, and `--assignee
""` is a usage error, not a way to clear — use `--unassign`.

`me` is a reserved value standing for the configured identity
(`NOTION_TRACK_ME`, or the profile's `me:`) — it's what "prendi in carico" /
"cosa devo fare io" / "take ownership" resolve to when the user means
themselves. If nothing is configured, `--assignee me` fails with a clear
message (exit 2); don't substitute a guessed name instead, ask the user or run
`doctor` to see what identity, if any, is set up.

If this board doesn't map an assignee column at all, `--assignee`/`--unassign`
fail (exit 1, not 2) telling you so — that's your cue to say the board has no
referente column, not to retry with a different flag.

### Mark how urgent a task is — `--priority`

Available on `upsert` and `set` to set it, and on `list` to filter by it. This
is what "è urgente", "priorità alta", "mettilo in alta", "cosa c'è di
urgente", "cosa faccio prima", "it's urgent", "high priority", or "what should
I do first" mean in practice:

```sh
notion-track set --ticket "<name>" --priority ALTA
notion-track set --ticket "<name>" --priority alta          # a partial value is enough when unambiguous
notion-track list --priority ALTA --status "<status>"       # what's urgent, optionally narrowed
notion-track list --priority ALTA --assignee me              # what's urgent that's mine
```

`--priority` resolves what you type against the board's priority column the
same way `--assignee` resolves a name — exact match, then case-insensitive,
then a case-insensitive substring — so `alta` is enough when only one option
contains it. **Don't invent a priority value**: pass what the user said and
let resolution match it against the board's real options; if it's ambiguous
or unknown the error lists the real options, which is more reliable than
guessing. Never assume a fixed vocabulary like ALTA/MEDIA/NORMALE applies —
read the actual values with `doctor` or `list` first.

**Unlike `--assignee`, this role has no way to clear a value, and no `me`.**
There is no `--unpriority` flag — nothing in this tool can remove a priority
once set; if asked to "remove the priority" or "togli la priorità", say that
has to be done in Notion directly, don't guess at a flag. There is also no
`list --unprioritized` (the priority equivalent of `--unassigned`), and no
reserved `me` value — a priority belongs to no one, so "prendi in carico"
never applies here.

If this board doesn't map a priority column at all, `--priority` fails (exit
1, not 2) telling you so — same as an unmapped assignee.

### Check first — `--dry-run`

```sh
notion-track upsert --ticket "<name>" --status "<status>" --dry-run [--json]
```

Available on `upsert`, `set` and `apply`. It reports what the write *would* do
— created or updated, which row, which columns — and writes nothing, exiting 0.
With `--json` the output is `{"dry_run":true,"plan":{...}}`, so you can tell it
apart from a write that happened.

It runs the same validation a real write does, so a status the board rejects
fails on the dry run rather than on the real one. That makes `--dry-run` the
cheapest way to answer "will this do what I think?" before touching the board.

### Writing the page body — `--body-file`

Both `upsert` and `set` accept `--body-file <path>` (`-` for stdin): a
Markdown file whose content becomes the page body. **Replace semantics —
`--body-file` owns the body.** Every run makes the body exactly equal to the
file, deleting whatever blocks were already there, hand-edited content
included; running it twice on the same file is idempotent, but running it
against a page someone has since edited in Notion silently discards their
edit. Read the page first if you're not sure what's on it. Sub-pages and
child databases nested under the page are preserved, not archived. See
`--body-file` under Usage in the [README](../../README.md) for the supported
Markdown subset, degrade-with-warning behavior, and cost.

Add **`--expand`** to substitute `{{ticket}}` and `{{date}}` (today, as
`YYYY-MM-DD`) in the file before it is sent. Without the flag those braces are
left exactly as written, so a body that legitimately contains `{{...}}` is safe
by default. A placeholder that is neither of the two known names is an error
naming the line, not something quietly passed through — so never invent
placeholder names.

### Read one task — `get`

```sh
notion-track get --ticket "<name>"     [--json]
notion-track get --id <board-id>       [--json]
notion-track get --page-id <id-or-url> [--json]
```

Use it to confirm a task exists and to see its current state before changing it.
With `--json` the fields are `id`, `ticket`, `title`, `status`, `page_id`, `url`,
`last_edited_time`, `assignee`, `priority` — a stable schema, safe to parse.
`id` is the board id (`TASK-271`), always present and empty both when the row
carries no value and when the board doesn't map that role — the same rule
`assignee` and `priority` follow next. `assignee` is always present and empty
both when nobody is assigned and when the board doesn't map the role, so check
for an empty string rather than a missing key. `priority` follows the same
rule: always present, empty both when the row carries no value and when the
board doesn't map the role.

### List tasks — `list`

```sh
notion-track list [--status "<status>"] [--assignee "<name-or-me>"] [--unassigned] [--priority "<value>"] [--json]
```

All rows, or narrowed by one status, by assignee, to only-unassigned rows
(`--assignee` and `--unassigned` are mutually exclusive), or by priority.
`--json` returns an **array** (`[]` when empty, never `null`), each element
with the same fields as `get`. This is the way to answer "what do I have in
progress?" or to find a task's page id — and, with `--assignee`/`--unassigned`,
to answer "chi ha in mano X" (`list --status "<status>"` then check `assignee`
per row, or `list --assignee "<name>"` directly), "cosa devo fare io" /
"what's on my plate" (`list --assignee me --status "<status>"`), and "task
senza referente" / "unassigned tasks" (`list --unassigned`). `--assignee`
resolves partial names and `me` exactly like it does on `upsert`/`set` — don't
guess a full name, pass what the user said. With `--priority`, it also answers
"cosa c'è di urgente" / "what's urgent" (`list --priority ALTA`) and "cosa
faccio prima" / "what should I do first" (narrow further with `--status` and,
if it's specifically the user's own work, `--assignee me`). There is no
`--unprioritized` — no shortcut for rows Notion ranks with nothing.

### Many changes at once — `apply`

```sh
notion-track apply --file <manifest.json|manifest.csv> [--dry-run] [--expand] [--json]
```

**Use this instead of looping over `set`/`upsert`.** One entry per write,
applied in order, in a single process.

```json
[
  {"op": "upsert", "ticket": "TASK-1", "title": "Hardening", "status": "In corso", "assignee": "sam", "priority": "alta"},
  {"op": "set", "ticket": "TASK-2", "status": "Fatto", "unassign": true}
]
```

The format comes from the file extension (`.json` or `.csv`; a CSV needs a
header row with the same field names). Fields: `op` (`upsert` or `set`,
defaulting to `upsert`), `ticket` (required), `title`, `status`, `due`,
`body_file`, `assignee`, `unassign`, `priority`. An unknown field is an error
rather than something ignored, so spell them exactly. `body_file` paths are
resolved relative to the manifest, not to the working directory. `assignee`
accepts the same partial names and `me` that `--assignee` does; `unassign` is
`true`/`false`/empty and conflicts with `assignee` on the same entry, the same
rule the flags enforce. `priority` accepts the same partial values that
`--priority` does; there is no `unpriority` field.

**It stops at the first entry that fails** and exits with that entry's own code
(3, 4, …), after reporting how many were applied — so a partial run is
something you can see and resume from, not something to reconstruct. With
`--json`: `{"applied":N,"total":M,"entries":[...]}`, where each entry carries
its `action` or its `error`.

Write the manifest to a file, run `apply --dry-run` first when you built it
yourself, and show the user the plan before applying it for real.

### Diagnose — `doctor`

```sh
notion-track doctor [--json]
```

Run this first if any command errors in a way you don't understand. It checks
the token, database access, the property mapping, duplicate keys, and whether a
git-tracked file in the current repository looks like it carries the user's
integration token — and prints what's wrong and how to fix it. When an
assignee column is mapped, it also checks that the configured `me` identity
still resolves to a real option, and warns if that identity only lives in the
committed config rather than `NOTION_TRACK_ME`. Only a `fail` makes it exit
non-zero; a `warn` (including the token scan and the identity check) is worth
reporting to the user but does not block anything.

## Exit codes — branch on these, don't parse messages

| Code | Meaning | What to do |
|---|---|---|
| 0 | success | proceed |
| 2 | bad usage (missing/invalid flag, unknown status, a malformed `--page-id` or `--id`, an `--assignee` value that's unknown or ambiguous, an empty `--assignee`, `--assignee me` with no identity configured, `--assignee` combined with `--unassign`/`--unassigned`, a `--priority` value that's unknown or ambiguous, or `--id` used on a board with no id column mapped) | fix the invocation; a rejected status, assignee or priority means the value isn't one the board allows — read the error's list of valid options rather than guessing again. **An unmapped id role is exit 2, not 1** — the one role that differs from the row below |
| 3 | task not found | with `set`/`get`: the ticket, board id, or page id doesn't match a row — don't retry as `upsert` without checking with the user |
| 4 | duplicate key | more than one row has that ticket key; the tool refuses to guess. Surface it and run `doctor` to list the duplicates |
| 5 | auth failure | the token is missing or invalid; tell the user to run `notion-track init` |
| 1 | other error, including an `--assignee`, `--priority` or `--due` role that simply isn't mapped on this board | report it; for one of these unmapped roles, say so and point at `init` rather than retrying. (Unmapped **id** is the exception — see exit 2 above.) |

`apply` reports the exit code of the entry that stopped it, so the same table
applies: a run that ends with 3 means one of its entries addressed a row that
does not exist, not that the manifest was malformed (that would be 2).

A rejected status (exit 2) is common and recoverable: the board accepts only a
fixed set of status values. Never invent one — use a value the board already
has (list them with `doctor` or by looking at existing tasks).

## Safe patterns

Change a task's status, checking it exists first:

```sh
notion-track get --ticket "Deploy staging" --json   # confirm it's there and see its state
notion-track set --ticket "Deploy staging" --status "Fatto"
```

Assign a task ("assegna a Sam") or take it yourself ("prendi in carico"):

```sh
notion-track get --ticket "Deploy staging" --json   # confirm it's there and who has it now
notion-track set --ticket "Deploy staging" --assignee sam
notion-track set --ticket "Deploy staging" --assignee me   # "prendi in carico" — needs an identity configured, see doctor
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

Answer "what am I working on?", "chi ha in mano X?", or "task senza referente":

```sh
notion-track list --status "In corso" --json                # what am I working on? (scoped by status)
notion-track list --assignee me --status "In corso" --json   # narrowed to mine
notion-track list --assignee "Sam Rivera" --json             # chi ha in mano X? / what's assigned to Sam
notion-track list --unassigned --json                        # task senza referente
```

Mark a task urgent and hand it off in one write ("è urgente" + "assegna a Sam"), then answer "cosa c'è di urgente":

```sh
notion-track set --ticket "Deploy staging" --priority alta --assignee sam
notion-track list --priority ALTA --json                     # cosa c'è di urgente, across the board
notion-track list --priority ALTA --assignee me --json        # cosa c'è di urgente ed è mio
```

Apply several changes the user asked for in one go — check, then commit to it:

```sh
cat > /tmp/changes.json <<'EOF'
[
  {"op": "set", "ticket": "Deploy staging", "status": "Fatto"},
  {"op": "set", "ticket": "Backup NAS", "status": "In corso"}
]
EOF
notion-track apply --file /tmp/changes.json --dry-run   # show this to the user
notion-track apply --file /tmp/changes.json
```

## Know this workspace before acting

This section is meant to be filled in for the board you're driving — status
values and the key mapping differ per workspace. **Don't assume; discover.** At
the start of a task session, or whenever a command fails in a way that suggests
the setup changed, run:

```sh
notion-track doctor          # token, database, property mapping, duplicates
notion-track list --json     # real rows, real status values in use
```

`doctor`'s `properties` check confirms each configured property still exists
with the expected type, naming the property and what's wrong when one doesn't
— it does not enumerate the mapped columns when everything checks out, only
reports that they do; the statuses actually present on the board are whatever
`list` returns. Two things to establish up front, because they change how you
address and create tasks:

- **Is the ticket key its own column, or the title?** If the key column *is* the
  title, then `--ticket "X"` means the task literally named X, and creating with
  `upsert --ticket "X"` sets its name to X — so a rename breaks lookup by name,
  and `--page-id` is the stable way to address such a task.
- **What status values does the board accept?** `--status` only takes an
  existing value; anything else is rejected with exit 2. Never invent one. No
  command prints the allowed set — `doctor` checks the column's type but never
  reads its options, and `list` shows only the values rows happen to carry. The
  rejection itself is the cheapest way to learn them: it names every accepted
  value ("unknown status "Done"; allowed values are: …"), so a wrong guess costs
  one failed call rather than a wrong write.
- **Is there an assignee column, and is `me` configured?** Not every board maps
  one, and `doctor` does not call that out when it's missing — an unmapped
  assignee role is skipped silently by the `properties` check, the same way an
  unmapped `id` role is; the only signal is `--assignee`/`--unassign` failing
  with exit 1. When it *is* mapped, `doctor` runs a dedicated `assignee` check
  (absent from the output otherwise) that says whether `me` resolves to a real
  identity, and whether that identity comes from `NOTION_TRACK_ME` or only
  from the committed config; if it doesn't resolve, "prendi in carico"/"assign
  it to me" needs the user to set `NOTION_TRACK_ME` first, not a guessed name.
- **Is there a priority column?** Not every board ranks urgency, and here too
  `doctor` stays silent when it's unmapped — `--priority` failing with exit 1
  is the only signal. Unlike assignee, priority never gets a dedicated check
  even when it *is* mapped: the `properties` check only confirms the column
  exists with the expected type, it does not list the accepted values. Read
  those from `list` — the values actually in use on real rows — rather than
  assuming a fixed set like ALTA/MEDIA/NORMALE applies here.
- **Is there a board id column?** Not every board maps one — `list --json` or
  `get --json` show a non-empty `id` key only when the role is mapped, and
  `--id` fails if it isn't mapped, but with **exit 2, not 1** (unlike
  assignee/priority/due, an unmapped id role is a usage error). When it is
  mapped, `--id` accepts the id exactly as Notion shows it (`TASK-271`) or the
  bare number alone (`271`).

- **Attribution caveat**: every change is recorded by the integration's bot
  identity, not by the person running the command. If the user asks "who moved
  this card", that information isn't captured.

> Using this on a fixed personal board? Replace this section with your board's
> concrete status values and mapping so the agent doesn't have to rediscover
> them every session.

## When NOT to reach for this skill

- Comments, arbitrary Notion pages, and other databases remain out of scope;
  `notion-track` only touches this one board. The page **body** is now
  writable via `upsert`/`set --body-file <file>` (Markdown, replace semantics
  — it **owns** the body and overwrites anything there, so read before you
  write). Sub-pages are preserved, not archived.
- Bulk changes across many tasks are now `apply`'s job, not a shell loop's:
  reach for a manifest rather than calling the binary once per row. A loop is
  only worth it when each row's flags depend on the previous row's output.
- The assignee is a single `select` value, not Notion's native "person" type —
  there's no team or multi-person assignment, and no lookup against Notion
  workspace members. `--assignee` only ever matches names the board's column
  already offers; don't try to resolve a Notion user id or invent a name it
  hasn't offered.
- Priority is a single `select` value too — a fixed vocabulary the board
  defines, not a numeric scale, and nobody's personal priority. There is no
  way to clear it through this tool (no `--unpriority`) and no
  `list --unprioritized`; if asked to do either, say so rather than trying a
  flag that doesn't exist.
- If your host speaks MCP, `notion-track mcp` serves the same operations as
  tools (`upsert_task`, `set_task`, `get_task`, `list_tasks`) over stdio, with
  the same JSON shapes documented here. It is the same code underneath, so
  everything on this page — read before you write, never invent a status,
  branch on the outcome — applies there unchanged, with one exception:
  addressing. Over MCP a row is addressed **only** by ticket key —
  `get_task`/`set_task`'s only argument for finding a row is `ticket`.
  `--id` and `--page-id` are CLI-only; there is no MCP equivalent for either,
  even though a tool's JSON result still carries the board id under `id`
  exactly like the CLI's `--json` does. Don't call an MCP tool with an `id`
  or `page_id` argument expecting it to address a row — it will be rejected
  (or, worse, ignored as an unknown field).
