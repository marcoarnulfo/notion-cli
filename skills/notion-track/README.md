# notion-track agent skill

A [Claude Code](https://claude.com/claude-code) skill that teaches an agent to
manage your Notion tasks through the `notion-track` CLI — create, update status,
read, list, and bulk-apply task rows — running the commands over the shell.

The tool is already agent-friendly on its own: `--json` everywhere with a stable
schema, differentiated exit codes, `--dry-run` on every write, and actionable
errors. This skill is what tells the agent *which* command to reach for and how
to stay safe (read before you write, check with `--dry-run`, never invent a
status, branch on exit codes).

## Shell or MCP

This skill drives the CLI over the shell, which needs nothing beyond the binary.
If your host speaks MCP instead, `notion-track mcp` serves the same operations
as tools over stdio — same code underneath, so the safety rules in `SKILL.md`
apply either way. Three things differ there, and `SKILL.md` spells them out
under "Over MCP instead of the shell": the result envelopes (`row` and `rows`,
where the CLI has `page` and a bare array), addressing (only by ticket key —
`--id` and `--page-id` have no MCP equivalent), and the absence of `dry_run`,
body writing and any `apply`/`doctor` tool. See also "Use it from an AI agent"
in the [README](../../README.md).

## Install and update

Copy `SKILL.md` into your Claude Code skills directory:

```sh
mkdir -p ~/.claude/skills/notion-track
cp skills/notion-track/SKILL.md ~/.claude/skills/notion-track/SKILL.md
```

The agent then picks it up automatically when you ask to touch a task on your
Notion board ("mark this done", "what am I working on?", "create a task…").

**Updating is the same copy, run again** — and it is on you to run it. The
installed file is a snapshot: it does not follow the tool, so upgrading the
binary with `go install …@latest` leaves the skill exactly as old as the day
you copied it. Without the repo checked out:

```sh
curl -fsSL https://raw.githubusercontent.com/marcoarnulfo/notion-cli/main/skills/notion-track/SKILL.md \
  -o ~/.claude/skills/notion-track/SKILL.md
```

A stale copy is quiet rather than broken, which is what makes it worth a
calendar reminder: the agent simply never reaches for the commands it doesn't
know about, and you see a capable tool behaving like a limited one. `SKILL.md`
tells the agent to flag the two symptoms it can notice — a documented flag the
binary rejects, or a `--help` entry the skill never mentions — but neither
shows up until you happen to ask for the affected thing. Re-copy after every
tool upgrade and the question doesn't arise.

## Requirements

- `notion-track` installed and on the `PATH` (`go install github.com/marcoarnulfo/notion-cli/cmd/notion-track@latest`).
- A configured profile and a token — run `notion-track init` once. The agent
  never handles the token; it only runs commands.

## Workspace-specific details

`SKILL.md` deliberately asserts nothing about your board: its "Know this board"
section lists the five things that differ per workspace — whether the ticket key
is the title, which status values exist, whether assignee, priority and board-id
columns are mapped — and tells the agent to discover them with
`notion-track doctor` and `notion-track list --json` at the start of a session.

If you run it against one fixed board, filling that section in with your real
values saves the agent a round of discovery every session. Keep it accurate:
stale values there are worse than none, because the agent trusts them.

## Keeping it honest

The claims `SKILL.md` makes about flags, commands, exit codes and JSON keys are
checked against the code by tests in `internal/cli`, `internal/mcp` and
`internal/manifest` (`skilldoc_test.go`). Adding a flag or renaming a JSON key
without updating the skill fails CI. Everything else — the prose, the ordering,
the judgement calls — is still on whoever edits it.
