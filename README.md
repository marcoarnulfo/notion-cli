# notion-track

> A small, opinionated Go CLI to keep a Notion task-tracking database in sync from your terminal — and from CI.

**Proposed binary name: `notion-track`** (repo: `notion-cli`). The repo name is generic, but the binary must not collide with the existing ecosystem: `ntn` is taken by Notion's official CLI and `notion-cli` is taken by the main community CLI (which even installs as `notion-cli` via Homebrew and npm). `notion-track` is unambiguous, self-describing (it tracks tasks, it does not "do Notion"), and greppable in CI logs. A shorter alias (`ntk`?) can be decided later — see [Open questions](#open-questions).

## The problem

Our team (Head of Tech + 2 developers) tracks work in a Notion database — one row per ticket or sub-task, with a Status property (`In corso`, `Fatto`, ...). Updating those rows by hand is repetitive and easy to forget; we want a repeatable way to do it from the command line, and eventually from CI pipelines, so that "flip the card to Done" is one command (or zero, when a pipeline does it).

The obvious modern answers don't work for us, for concrete reasons:

- **Notion MCP / user OAuth is broken for workspace guests.** The OAuth consent flow asks for workspace-level authorization that a *guest* user cannot grant. Two of us access the workspace as guests, so any tool built on user login is a non-starter.
- **The MCP endpoint is blocked by corporate firewalls.** Some firewalls (e.g. Cato Networks) categorize Notion's MCP endpoint under "Generative AI Tools / Remote MCP" and block it outright.
- **Plain `api.notion.com` passes those same firewalls** and is reachable from everywhere we work.
- **A Notion internal integration token (`ntn_...`) sidesteps all of this.** The integration is a *bot with its own permissions*: it works identically for guests and members, requires no browser login, and — least-privilege by design — only sees the databases a Workspace Owner explicitly shares with it. One owner creates the integration once, shares the tracking database with it, and everyone (humans and CI) uses the token.

So the constraint set is clear: **HTTPS to `api.notion.com`, authenticated with an integration token from an environment variable or config file, never committed.** That is exactly what this tool is built around.

## What it is

`notion-track` is a **task-tracking client for Notion**, not a Notion client. It knows about one thing: a database where each row is a ticket, identified by a ticket key (e.g. `BDF-231-SubD`), with a Status and a handful of other properties. Its core operation is an **idempotent upsert**: given a ticket key, find the row and update it, or create it if it doesn't exist. Run it twice, get one row.

Vision:

- **One command per intent.** "Mark BDF-231 done" is one line, not a `curl` with a JSON filter body.
- **Idempotent by construction.** Safe to re-run, safe to put in a retried CI job.
- **Zero config beyond the token and the database ID.** Sensible defaults, a tiny config file, no project scaffolding.
- **CI-first output.** Quiet on success, meaningful exit codes, `--json` when a machine is reading.
- **A single static binary.** Written in Go: no Node runtime, no Python venv, `scp` it anywhere. It's also the team's project for consolidating our Go skills in the open.

## Non-goals

- **Not a general-purpose Notion CLI.** No blocks API, no comments, no search, no user management, no arbitrary page trees. [`4ier/notion-cli`](https://github.com/4ier/notion-cli) already covers the full API surface well; we won't re-implement it.
- **No Notion Workers.** Deploying/managing Workers is the official [`ntn`](https://developers.notion.com/cli/get-started/overview) CLI's territory.
- **No browser-based OAuth, ever.** Auth is a token in the environment or a config file, full stop (see [The problem](#the-problem)).
- **Not a sync engine.** It pushes single-row changes on demand; it does not mirror Jira/GitHub into Notion or watch for changes.

## Core features (MVP)

1. **Idempotent upsert of a ticket row** — query the database for the row whose Ticket property equals the key; update it if found, create it otherwise. `0 → create`, `1 → update`, `>1 → error` (duplicates are a data problem the tool refuses to worsen).
2. **Property setting** — Status (select), title, dates, plain-text fields; page body from a Markdown file.
3. **Token auth** — `NOTION_TOKEN` env var, or config file fallback.
4. **CI-friendly behavior** — non-zero exit on failure, `--json` output, no interactive prompts.

### Planned commands

```sh
# Create-or-update the row for a ticket (the flagship command)
notion-track upsert --ticket BDF-231-SubD \
  --title "Hardening rotaia AI" \
  --status "In corso" \
  --body-file notes.md

# Just flip a property on an existing row (errors if the row doesn't exist)
notion-track set --ticket BDF-231-SubD --status "Fatto"

# Read a row (human-readable, or --json for scripts)
notion-track get --ticket BDF-231-SubD

# List rows by status
notion-track list --status "In corso"

# Sanity-check token, database access, and required properties
notion-track doctor
```

```sh
# Typical CI usage (GitHub Actions step)
- run: notion-track upsert --ticket "$TICKET" --status "Fatto"
  env:
    NOTION_TOKEN: ${{ secrets.NOTION_TOKEN }}
    NOTION_TRACK_DB: ${{ vars.NOTION_TRACK_DB }}
```

## Design & tech decisions

**Language: Go.** Single static cross-platform binary, trivial distribution to teammates and CI runners, and a deliberate team investment in learning Go on a real open-source project.

**CLI framework: [`urfave/cli`](https://github.com/urfave/cli) (v3).** For a tool with a flat set of five-ish commands, urfave/cli's declarative, single-file style keeps the code small and readable. [`spf13/cobra`](https://github.com/spf13/cobra) is the heavyweight standard (nested commands, generators, Viper integration) and would be the right call for a large CLI, but its per-command-file scaffolding is overhead we don't need; community comparisons consistently position urfave/cli as the lighter fit for simple tools. If the command tree ever grows deep, migrating is mechanical.

**Notion API: [`jomei/notionapi`](https://github.com/jomei/notionapi) behind a thin internal interface.** It's an actively maintained, typed Go SDK with **zero dependencies beyond the standard library** — the typed property-value models (select, date, rich text) are exactly the fiddly part we don't want to hand-roll. Caveat, stated honestly: it targets Notion API version `2022-06-28`, while Notion's 2025+ API versions introduced multi-source databases ("data sources"). Our MVP touches only three endpoints (database query, page create, page update), all stable on the pinned version — and by keeping the SDK behind a small internal `tracker` interface, swapping in a hand-rolled `net/http` client with a newer `Notion-Version` header later is a contained change, not a rewrite.

**Config & token handling.**

- Token: `NOTION_TOKEN` env var first; fallback to `~/.config/notion-track/config.toml` (`XDG_CONFIG_HOME` respected). **Never in the repo** — `doctor` will warn if it finds a token-looking string in a tracked file.
- Database: `NOTION_TRACK_DB` env var or `database_id` in the config file.
- The integration token is created by a Workspace Owner and shared **only with the tracking database** (Notion's integration model enforces this) — the blast radius of a leaked token is one database, not the workspace.

**Idempotency.** The ticket key is the natural key. `upsert` runs a database query filtered on the Ticket property, then updates or creates. No local state, no cache file: the database itself is the source of truth, so concurrent runs from a laptop and a CI job converge.

## Prior art & alternatives

We looked hard at the existing tools before starting this. Both are good; neither is shaped like our problem.

| | [`ntn`](https://developers.notion.com/cli/get-started/overview) (official) | [`4ier/notion-cli`](https://github.com/4ier/notion-cli) | `notion-track` (this project) |
|---|---|---|---|
| Origin | Notion, 2026 Developer Platform (beta) | Community, Go | Us |
| Scope | API requests, Workers, data sources, markdown pages | Full Notion API, 39 commands ("`gh` for Notion") | Task-tracking rows only |
| Database-row ergonomics | Low-level (raw API requests for row upserts) | High-level generic CRUD (`page create`, `db query`) | Purpose-built `upsert`/`set` per ticket |
| Idempotent upsert by key | No — compose it yourself | No — compose it yourself | **Yes, the core primitive** |
| Auth | `ntn login` via browser (guest problem); `NOTION_API_TOKEN` as workaround | Integration token | Integration token, **only** mode |
| Windows | Not supported (macOS/Linux) | Yes | Yes (Go cross-compile) |
| Fit for our CI/guest/firewall constraints | Partial, with workarounds | Workable but generic | Designed for them |

The honest summary: if you need the whole Notion API from a terminal, use `4ier/notion-cli`; if you build Notion Workers, use `ntn`. If you want `upsert --ticket X --status Done` to be one safe, repeatable command for a tracking database — that's us.

## Distribution

- **GitHub Releases** — prebuilt static binaries for macOS/Linux/Windows (amd64/arm64), built with GoReleaser.
- **`go install github.com/<org>/notion-cli/cmd/notion-track@latest`** — for Go-equipped machines.
- **Homebrew tap** — candidate for after v1, if there's demand beyond the team.
- **CI** — a thin composite GitHub Action wrapping the binary (`uses: <org>/notion-cli/action@v1`), so pipelines get `upsert` without install boilerplate.

## Roadmap

- **v0.1 (MVP)** — `upsert`, `set`, `get`, `doctor`; token/config handling; single database.
- **v0.2** — `list`, `--json` everywhere, GitHub Action, GoReleaser pipeline.
- **v0.3** — multi-database profiles in config (`--profile work`), body templating (ticket key/date placeholders in the Markdown body).
- **Later / maybe** — bulk operations from a CSV/JSON manifest, `--dry-run`, native support for newer Notion API versions (data sources) if/when we outgrow the pinned SDK version.

## Open questions

- **Binary name** — `notion-track` proposed here; do we also ship a short alias (`ntk`)? Decide before v0.1 tags.
- **v1 property set** — Ticket (title or rich_text?), Status (select vs Notion's native status type), Title, due date, free-text notes. Which are required vs optional flags?
- **Duplicate policy** — `upsert` errors on >1 match (current proposal). Alternative: update the most recent and warn. Erroring is safer; confirm.
- **Homebrew tap** — worth the maintenance for a 3-person team + open-source users? Defer until someone asks.
- **License** — **MIT proposed**: permissive, zero-friction for a small tool we want people to embed in CI, consistent with the Go CLI ecosystem (cobra, urfave/cli are MIT/Apache-family). Confirm before first public release.

## References

- Notion CLI (`ntn`) — official docs: <https://developers.notion.com/cli/get-started/overview>
- Notion 3.5 release notes (Developer Platform / `ntn` launch, May 13 2026): <https://www.notion.com/releases/2026-05-13>
- `4ier/notion-cli` (community Go CLI, full API coverage): <https://github.com/4ier/notion-cli>
- `jomei/notionapi` (Go SDK for the Notion API): <https://github.com/jomei/notionapi> — package docs: <https://pkg.go.dev/github.com/jomei/notionapi>
- `urfave/cli`: <https://github.com/urfave/cli>
- `spf13/cobra`: <https://github.com/spf13/cobra>
- Go CLI library comparison: <https://github.com/gschauer/go-cli-comparison>
- Notion authorization model (internal integrations & tokens): <https://developers.notion.com/docs/authorization>
