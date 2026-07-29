# Setup notion-track

Installs the [`notion-track`](https://github.com/marcoarnulfo/notion-cli) CLI from a published release, verifies it against the release's `checksums.txt`, and puts it on `PATH`.

A workflow step that flips a task's status should not need a Go toolchain and a compile to do it.

## Usage

```yaml
- uses: marcoarnulfo/notion-cli/action@main
  with:
    version: v0.6.0        # or "latest"

- run: notion-track upsert --ticket "$TICKET" --status "Done" --config notion-track.yml
  env:
    NOTION_TOKEN: ${{ secrets.NOTION_TOKEN }}
```

## Inputs

| Input | Default | Description |
|---|---|---|
| `version` | `latest` | Release tag to install, or `latest` for the newest published release. `latest` fails if the repository has only prereleases — pin a tag in that case. |
| `token` | `${{ github.token }}` | Token used to resolve and download the release. Override it when the job restricts permissions, when the default token is rate-limited, or on GitHub Enterprise. |
| `repository` | `marcoarnulfo/notion-cli` | Repository to install from. Only useful for a fork. |

## Outputs

| Output | Description |
|---|---|
| `version` | The release tag that was installed, with `latest` resolved |

## What it verifies

The archive is checked against its own line in the release's `checksums.txt`, and a missing line is an error rather than a pass. That is deliberate: `sha256sum --check --ignore-missing` iterates the checksum file rather than the downloaded files, so an archive that is present but unlisted would never be checked — and on some macOS images that spelling exits 0 without verifying anything at all.

What this proves is that the bytes match what the release published. It does not prove who published them; signing and provenance attestation would, and are not in place yet.

## Requirements

- **Linux, macOS or Windows runners**, amd64 or arm64. Windows goes through Git Bash, and unpacking the `.zip` needs one of `unzip`, `7z` or a `tar` that reads zip — the GitHub-hosted images have `7z`. Anything else fails with an explicit message rather than installing something wrong.
- **The GitHub CLI (`gh`)**, which is preinstalled on GitHub-hosted runners but not inside `container:` jobs or on most self-hosted runners. The action checks for it up front and says so.
- **A published release matching `version` to download from.** The action needs at least one `v*` tag pushed to this repository before there's anything to install; `go install github.com/marcoarnulfo/notion-cli/cmd/notion-track@latest` needs no release at all and always works.

## Pinning

`@main` is a moving reference: you get whatever is on that branch when your workflow runs. Pin to a commit SHA for a workflow that cannot change under you. A `@v1` tag will exist once the project reaches 1.0.
