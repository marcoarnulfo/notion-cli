# Security Policy

## Reporting a vulnerability

If you find a security issue in `notion-track`, please **do not open a public issue**. Instead, report it privately to the maintainer [@marcoarnulfo](https://github.com/marcoarnulfo) — either via a [GitHub Security Advisory](https://github.com/marcoarnulfo/notion-cli/security/advisories/new) on this repository, or by contacting the maintainer directly through their GitHub profile. Please include:

- What you found and why it's a security issue.
- Steps to reproduce, or a minimal example.
- The affected version/commit.

You should get an acknowledgement within a few days. There is no bug bounty — this is a small open-source project maintained on a best-effort basis — but confirmed reports will be credited (unless you'd rather stay anonymous) once a fix ships.

## What's in scope

- The `notion-track` binary and its source in this repository.
- Anything that could leak the Notion integration token, corrupt the config file, or make requests to something other than `api.notion.com`.

Vulnerabilities in Notion's own API or web app are Notion's to fix — report those to Notion directly.

## The integration token is a shared secret

`notion-track` authenticates with a single Notion **internal integration token** (`ntn_...`). Treat it exactly like a password:

- It is read **only** from the `NOTION_TOKEN` environment variable — never from the config file, never from a flag, never logged. `notion-track`'s own config file cannot leak the token because it never contains one.
- Anyone holding the token can do everything the integration is permitted to do: read, create and update every row in every data source the integration has been shared with — nothing more, nothing less.
- **Blast radius is scoped by design.** A Notion integration only sees the databases a Workspace Owner has explicitly shared with it (Notion's authorization model enforces this, not `notion-track`). A leaked token exposes those shared databases, not the whole workspace.
- Never commit a token to a repository, paste it into an issue, or put it in a config file that gets committed — the config file this tool writes and reads is designed to hold no secret specifically so it's safe to commit.

## Rotating a token

If a token leaks, or you simply want to rotate it on a schedule:

1. Go to <https://www.notion.so/my-integrations> and **revoke** the compromised integration (this immediately cuts off all access — the fastest way to stop an active leak). Only a Workspace Owner can do this.
2. Create a **new** internal integration and re-share the tracking database(s) with it, the same way as during initial setup.
3. Update every place the old token was stored:
   - CI secrets (e.g. the repository's `NOTION_TOKEN` GitHub Actions secret).
   - Every developer's local shell environment / shell profile / secret manager.
4. Confirm the new token works: `NOTION_TOKEN=<new token> notion-track doctor`.

The config file (`config.yml`) needs no changes during rotation — it holds no token, only the data source id and property mapping, so nothing there depends on which token is currently in use.

## Supported versions

This project has not yet reached a `v1.0.0` tag. Security fixes are made against the latest commit on the default branch; there is no separate maintenance branch for older releases at this stage.
