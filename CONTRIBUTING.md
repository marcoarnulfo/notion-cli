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

To try it against a real workspace, set the token via the environment (never write it to `config.yml` — see the `internal/config` conventions below for why):

```bash
export NOTION_TOKEN=ntn_...
go run ./cmd/notion-track doctor
```

Alternatively, run `notion-track init` at an interactive terminal without `NOTION_TOKEN` set and it will prompt for the token and offer to save it to `credentials.yml`, so you don't have to re-export it for every `go run` during a dev session.

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
internal/markdown    PURE domain: Markdown → Notion block tree, chunking limits
internal/template    PURE domain: {{ticket}}/{{date}} placeholder expansion for --body-file
internal/manifest    PURE domain: the JSON/CSV bulk manifest `apply` reads
internal/config      config.yml (profiles, property mapping), credentials.yml (token + per-profile identities) + NOTION_TOKEN / NOTION_TRACK_* env vars
internal/tui         bubbletea models (init wizard, browsing TUI); no network — calls arrive injected, so screens are testable without a terminal
internal/secrets     scans git-tracked files for token-shaped strings, for doctor's "secrets" check
```

Two rules here are **not negotiable**:

> `internal/tracker` and `internal/markdown` must stay pure: no I/O, and no dependency on `internal/service` or `internal/cli`. `internal/tracker` may import `internal/notion` and `internal/config` for their data types only — domain logic goes there so it can be tested without mocks.

> Only stdlib in tests. No testify, no gomock. Fake the API with `httptest.Server`.

Other conventions worth knowing:

- `internal/cli` never calls `os.Exit`; `Execute()` returns an int exit code so the whole command tree can be exercised in-process in tests. Only `cmd/notion-track/main.go` is allowed to exit the process.
- Exit codes are a public contract (see the README's [Exit codes](README.md#exit-codes) table) — map a new failure mode onto an existing code rather than inventing one casually, and update the table if you do add one.
- Any JSON key printed with `--json` is a stable scripting contract too — renaming or removing one is a breaking change, documented as such.
- `internal/config`: a token read from `NOTION_TOKEN` must never be written to disk — `Save`/`SaveTo` (config.yml) must stay silent about it, and `SaveToken` (credentials.yml) must never be called with an env-sourced token. `config.yml` itself must never carry a token, full stop — that's what `credentials.yml` is for, and only an interactive, explicit user opt-in (`init`'s save prompt) may write to it.
- `internal/config`: the same reasoning covers the `--assignee me` identity, which is personal and lives in `credentials.yml`. Nothing may write `me:` back into `config.yml` — the field is read-only legacy, kept so existing configs keep working, and `doctor` reports it. No command may rewrite `config.yml` as a side effect either: it is meant to be committed, and an unexplained diff in someone's `git status` is how a personal value ends up shared again.

## Commit & PR guidelines

- Use **[Conventional Commits](https://www.conventionalcommits.org) with a scope**, e.g. `feat(cli): add list --status filter`, `fix(tracker): guard duplicate detection`, `docs(readme): document exit codes`. Look at `git log` for the established scopes (`cli`, `service`, `notion`, `tracker`, `config`, `docs`, ...).
- **Never add `Co-Authored-By` to a commit message**, regardless of what tooling you used to help write the change.
- Any change to observable behavior (a flag, an exit code, a JSON key, the config shape) must update **both** `README.md` and `README.it.md` in the same PR — not just the primary one.
- Keep PRs focused; fill in the PR template and link the issue (`Closes #N`).
- Make sure the checks above pass before requesting review.

## Cutting a release

For maintainers. Publishing is one command; everything after it is automated.

```bash
git checkout main && git pull
git status --short          # must be empty

gofmt -l .                  # must print nothing
go vet ./... && go build ./... && go test ./... -race
go run honnef.co/go/tools/cmd/staticcheck@latest ./...

git tag -a v0.7.1 -m "what changed"
git push origin v0.7.1
```

Run those checks even though the release workflow re-runs most of them: it deliberately skips `staticcheck`, because fetching an unpinned tool inside the job that publishes executables would give back the supply-chain property its pinned SHAs exist to protect.

**Which number.** While the project is `0.x`: the third digit for fixes, the second for features. `v0.6.0` → `v0.6.1` was a fix; `v0.6.1` → `v0.7.0` added the identity move.

**The tag is the trigger**, and it is the only one — merging to `main` publishes nothing. The workflow matches `vX.Y.Z` and `vX.Y.Z-suffix` only, so a moving major tag like `v1` (the kind a composite action is consumed through) can be repointed without firing a release. A hyphen suffix publishes a prerelease, which `latest` skips — useful for exercising the pipeline without affecting anyone.

Pushing the tag builds the six archives and `checksums.txt`, publishes the release with notes generated from the commits since the previous tag, and then installs that exact tag with the composite action on Linux, macOS and Windows. A failure in that last job means the archives are public and unusable — the run goes red.

Then confirm the path users actually take:

```bash
go install github.com/marcoarnulfo/notion-cli/cmd/notion-track@latest
notion-track --version
```

If it still reports the previous version, the local module cache is holding a stale version list; rerun it shortly, or ask for the tag explicitly.

Two things that cannot be undone: a published release's notes are fixed, so a correction to the footer in `.goreleaser.yaml` only reaches the *next* release; and a tag anyone has fetched as a Go module stays resolvable through `proxy.golang.org` even after you delete it here. Neither is a reason to re-tag — cut the next patch instead.

Thank you for helping make notion-track better!
