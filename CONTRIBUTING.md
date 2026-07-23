**English** · [Italiano](CONTRIBUTING.it.md)

# Contributing to notion-track

Thanks for your interest — contributions of all sizes are welcome! Bug reports, docs, and code are all appreciated. This project is free and open-source (MIT).

By participating you agree to our [Code of Conduct](CODE_OF_CONDUCT.md).

## Ways to contribute

- 🐛 **Report a bug** or 💡 **propose a feature** via [Issues](https://github.com/marcoarnulfo/notion-cli/issues) (templates provided).
- 🧑‍💻 **Send a PR** — for anything non-trivial, please **open an issue first** so we can align on the approach before you invest time.
- 📖 Improve the docs — the README is bilingual: English `README.md` + `README.it.md`.

Found a security issue instead? See [SECURITY.md](SECURITY.md) — please don't open a public issue for those.

## Prerequisites

- **[Go](https://go.dev/dl/) 1.26+** (`go version` to check).
- [`staticcheck`](https://staticcheck.dev) for linting (optional locally, run in CI):
  `go install honnef.co/go/tools/cmd/staticcheck@latest`.
- A Notion internal integration token if you want to exercise the tool against real data — see the main [README](README.md#quick-start) for how to create one. It is not required to build, vet, or run the test suite: every test fakes the Notion API with `httptest.Server`.

## Development setup

```bash
git clone https://github.com/marcoarnulfo/notion-cli.git
cd notion-cli
go build ./...
go run ./cmd/notion-track --help
```

To try it against a real workspace, set the token via the environment (never write it to the config file):

```bash
export NOTION_TOKEN=ntn_...
go run ./cmd/notion-track doctor
```

## Before opening a PR

Run the same checks CI runs — all must be clean/green:

```bash
gofmt -l .                                          # no output = formatted
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go test ./... -race
go build ./...
```

## Project layout & conventions

```
cmd/notion-track     entry point (binary: notion-track); the only place allowed to call os.Exit
internal/cli         cobra command tree, flag parsing, JSON rendering, exit-code mapping
internal/service     orchestrates client + config + domain for one profile (Upsert/Set/Get/List/Doctor)
internal/notion      Notion API client (net/http only — no SDK), retry/backoff, typed errors
internal/tracker     PURE domain: create-or-update decisions, payload building, status validation
internal/config      YAML config (profiles, property mapping) + NOTION_TOKEN / NOTION_TRACK_* env vars
```

Two rules here are **not negotiable**:

> `internal/tracker` and `internal/markdown` must stay pure: no I/O, no imports of `internal/notion` or `internal/config`. Domain logic goes there so it can be tested without mocks.

> Only stdlib in tests. No testify, no gomock. Fake the API with `httptest.Server`.

Other conventions worth knowing:

- `internal/cli` never calls `os.Exit`; `Execute()` returns an int exit code so the whole command tree can be exercised in-process in tests. Only `cmd/notion-track/main.go` is allowed to exit the process.
- Exit codes are a public contract (see the README's [Exit codes](README.md#exit-codes) table) — map a new failure mode onto an existing code rather than inventing one casually, and update the table if you do add one.
- Any JSON key printed with `--json` is a stable scripting contract too — renaming or removing one is a breaking change, documented as such.
- `internal/config`: a token read from `NOTION_TOKEN` must never be written back to the config file — `Save`/`SaveTo` must stay silent about it. Don't add a code path that persists a token.

## Commit & PR guidelines

- Use **[Conventional Commits](https://www.conventionalcommits.org) with a scope**, e.g. `feat(cli): add list --status filter`, `fix(tracker): guard duplicate detection`, `docs(readme): document exit codes`. Look at `git log` for the established scopes (`cli`, `service`, `notion`, `tracker`, `config`, `docs`, ...).
- **Never add `Co-Authored-By` to a commit message**, regardless of what tooling you used to help write the change.
- Any change to observable behavior (a flag, an exit code, a JSON key, the config shape) must update **both** `README.md` and `README.it.md` in the same PR — not just the primary one.
- Keep PRs focused; fill in the PR template and link the issue (`Closes #N`).
- Make sure the checks above pass before requesting review.

Thank you for helping make notion-track better!
