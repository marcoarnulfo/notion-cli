# notion-track agent skill

A [Claude Code](https://claude.com/claude-code) skill that teaches an agent to
manage your Notion tasks through the `notion-track` CLI — create, update status,
read, and list task rows — running the commands over the shell.

The tool is already agent-friendly on its own: `--json` everywhere with a stable
schema, differentiated exit codes, and actionable errors. This skill is what
tells the agent *which* command to reach for and how to stay safe (read before
you write, never invent a status, branch on exit codes).

## Install

Copy `SKILL.md` into your Claude Code skills directory:

```sh
mkdir -p ~/.claude/skills/notion-track
cp skills/notion-track/SKILL.md ~/.claude/skills/notion-track/SKILL.md
```

The agent then picks it up automatically when you ask to touch a task on your
Notion board ("mark this done", "what am I working on?", "create a task…").

## Requirements

- `notion-track` installed and on the `PATH` (`go install github.com/marcoarnulfo/notion-cli/cmd/notion-track@latest`).
- A configured profile and a token — run `notion-track init` once. The agent
  never handles the token; it only runs commands.

## Workspace-specific details

The "This workspace" section of `SKILL.md` describes one particular board (its
status values, its key column). If you install this on a different setup, edit
that section — or delete it and let the agent rediscover the board with
`notion-track doctor` and `notion-track list --json`.
