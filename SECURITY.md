# Security Policy

## Reporting a vulnerability

If you find a security issue in `notion-track`, please **do not open a public issue**. Instead, report it privately to the maintainer [@marcoarnulfo](https://github.com/marcoarnulfo) — either via a [GitHub Security Advisory](https://github.com/marcoarnulfo/notion-cli/security/advisories/new) on this repository, or by contacting the maintainer directly through their GitHub profile. Please include:

- What you found and why it's a security issue.
- Steps to reproduce, or a minimal example.
- The affected version/commit.

You should get an acknowledgement within a few days. There is no bug bounty — this is a small open-source project maintained on a best-effort basis — but confirmed reports will be credited (unless you'd rather stay anonymous) once a fix ships.

## What's in scope

- The `notion-track` binary and its source in this repository.
- Anything that could leak the Notion integration token, corrupt the config or credentials files, or make requests to something other than `api.notion.com`.

Vulnerabilities in Notion's own API or web app are Notion's to fix — report those to Notion directly.

## The integration token is a shared secret

`notion-track` authenticates with a single Notion **internal integration token** (`ntn_...`). Treat it exactly like a password:

- It is read from the `NOTION_TOKEN` environment variable first, then from `credentials.yml` — never from a flag, never logged, and never from `config.yml`, which is designed to hold no secret specifically so it's always safe to commit. `credentials.yml` is a separate file for exactly this reason: it exists so the token has somewhere to be persisted that isn't the file meant for a repository.
- `credentials.yml` is written in exactly one place: an interactive `init` run, after you explicitly accept the "save it?" prompt. It is written with `0600` permissions and replaced atomically. Nothing writes a token there without that explicit opt-in, and a token read from `NOTION_TOKEN` is never written back to it — a CI secret can't leak onto disk through a normal run.
- Anyone holding the token can do everything the integration is permitted to do: read, create and update every row in every data source the integration has been shared with — nothing more, nothing less.
- **Blast radius is scoped by design.** A Notion integration only sees the databases a Workspace Owner has explicitly shared with it (Notion's authorization model enforces this, not `notion-track`). A leaked token exposes those shared databases, not the whole workspace.
- Never commit a token to a repository, paste it into an issue, or commit `credentials.yml` — that file exists specifically to hold what `config.yml` cannot, and is not meant to travel with a project's repository. `config.yml` remains safe to commit; it never contains a token.

## Rotating a token

If a token leaks, or you simply want to rotate it on a schedule:

1. Go to <https://www.notion.so/my-integrations> and **revoke** the compromised integration (this immediately cuts off all access — the fastest way to stop an active leak). Only a Workspace Owner can do this.
2. Create a **new** internal integration and re-share the tracking database(s) with it, the same way as during initial setup.
3. Update every place the old token was stored:
   - CI secrets (e.g. the repository's `NOTION_TOKEN` GitHub Actions secret).
   - Every developer's local shell environment / shell profile / secret manager.
   - Every developer's `credentials.yml`, if they saved the old token there — re-run `notion-track init` interactively and accept the save prompt to overwrite it, or edit the file directly (it's plain YAML with `0600` permissions).
4. Confirm the new token works: `NOTION_TOKEN=<new token> notion-track doctor` — or just `notion-track doctor` once `credentials.yml` has been updated; either way, the `token` check's detail names which source doctor actually used.

The config file (`config.yml`) needs no changes during rotation — it holds no token, only the data source id and property mapping, so nothing there depends on which token is currently in use.

## Supported versions

This project has not yet reached a `v1.0.0` tag. Security fixes are made against the latest commit on the default branch; there is no separate maintenance branch for older releases at this stage.
