# Moving the personal identity out of the shared config — design

**Date:** 2026-07-30
**Status:** approved, ready for an implementation plan

## 1. The problem

`--assignee me` needs to know who "me" is. Today that lives in `Profile.Me`
(`internal/config/config.go:70`), a field of `config.yml` — the file the
project is meant to commit and share.

A personal value in a shared file cannot be made safe, so the codebase
compensates for it in four places:

- `init --me` prints a warning about the value it has just written
  (`internal/cli/init.go:351`).
- `doctor`'s `assignee` check warns when the identity came from the file
  rather than the environment variable (`internal/service/doctor.go`).
- Both READMEs carry a paragraph explaining why `NOTION_TRACK_ME` is "the one
  to actually use".
- Every user is told to hand-edit their shell profile.

Four mechanisms exist to work around one field being in the wrong file. The
failure they guard against is real and silent: a teammate who never exports
`NOTION_TRACK_ME` resolves `me` to whoever ran `init`, and assigns work to
that person without any error.

## 2. Why the fix is structural

The repository already solved this exact problem once. From
`internal/config/config.go:195`:

> It exists as a file separate from config.yml on purpose: config.yml holds no
> secret and is meant to be committed to a project repo so CI and every
> teammate share the same property mapping. […] Splitting the files makes
> "never commit the token" a property of the filesystem layout instead of a
> rule a human has to remember to follow.

The identity is the same class of data as the token: per-user, never shared,
meaningless to a teammate. It belongs in the same per-user file,
`credentials.yml` under `os.UserConfigDir()`.

The asymmetry is visible inside `init.go` itself. The token flow offers to
save to `credentials.yml`, and only when the user declines does it print an
`export` line — with the honest note that "a child process can't modify its
parent shell's environment" (`internal/cli/init.go:110`). The identity flow
skips the offer and goes straight to the lecture.

**This design deletes the workarounds rather than improving them.** A warning
that no longer needs to exist is better than a clearer warning.

## 3. What changes

The identity moves to `credentials.yml`. `NOTION_TRACK_ME` stays as an
override — CI and containers need it, and nothing about it was wrong.
`Profile.Me` stays readable forever as a legacy source, so no existing
`config.yml` breaks.

### 3.1 On-disk shape

```yaml
schema_version: 1
token: secret_...
identities:
  default: Marco Arnulfo
  work: marco
```

Keyed by profile name, not a single string. `Profile.Me` is per-profile
today; two profiles can point at workspaces that spell the same person
differently. Collapsing to one global value would remove a capability that
exists, silently, for users who would only find out when a write went to the
wrong person.

`identities` is omitted entirely when empty, exactly as `Properties.ID` and
the other optional fields are.

### 3.2 No schema-version bump

`CurrentSchemaVersion` stays at 1. The addition is purely additive and
`gopkg.in/yaml.v3` ignores unknown keys by default (nothing in
`internal/config` sets `KnownFields`), so a `credentials.yml` carrying
`identities` is readable by a v0.6.1 binary — it simply ignores it.

Bumping would be actively harmful: `migrate` (`internal/config/migrate.go:17`)
rejects a file whose version exceeds the binary's, and `CurrentSchemaVersion`
is shared with `config.yml`. Bumping it would make every config written by a
new binary unreadable to a teammate still on the old one, for a change that
needs no migration at all. This follows the precedent set when
`Properties.ID` was added: additive field, no bump.

### 3.3 Precedence, in exactly one place

```
NOTION_TRACK_ME  →  credentials.yml identities[profile]  →  config.yml me:  →  unset
```

The environment variable keeps winning: CI passes an identity that must not
be read off disk, and a user overriding for one command must not have to edit
a file.

`Resolve` currently applies `MeEnv` onto the profile
(`internal/config/config.go:178-180`). That line moves into a single
identity-resolution function so the whole precedence chain is readable at one
call site. Splitting precedence across two files is how the third source ends
up ignored by one of them.

The resolver must report **which source** the value came from, because
`doctor` and `init` both need to say so, and because that is what makes the
legacy warning in §3.6 possible.

### 3.4 `init --me` writes to the per-user file

`--me` keeps its current validation — resolving the value against the
assignee column's options (`internal/cli/init.go:343`), so a typo cannot
reach disk — and keeps saving the canonical spelling. What changes is the
destination and the outcome:

- it writes `identities[<profile>]` in `credentials.yml`
- it prints a confirmation naming that file, in the shape the token save
  already uses
- **the warning at `init.go:351` is deleted.** There is nothing left to warn
  about.

`init --me` must never write `me:` into `config.yml` again.

### 3.5 The interactive wizard asks for the identity

The TUI wizard has no identity step at all today: `--me` is flag-only, so a
user who runs `notion-track init` without flags is never offered one, and
discovers `--assignee me` is unavailable only when it fails.

The wizard gains one optional step, shown **only when the assignee role is
mapped** — an identity is meaningless without a column to resolve it against.
Skipping it is a first-class outcome, not a failure: identity is optional and
`doctor` already reports its absence as `ok`.

The value is validated against the mapped column's options exactly as `--me`
is, so the two entry points cannot disagree about what a valid identity is.

### 3.6 `doctor` reports the source, and flags the legacy one

`checkAssignee` (`internal/service/doctor.go:152`) keeps verifying that the
identity still resolves to an option the column offers. Two changes:

- The existing warning "the identity comes from the config file rather than
  the environment variable" is **replaced**: the config file is no longer the
  recommended-against source in general, only the *legacy* one. It now warns
  only when the value came from `config.yml`'s `me:`, and the fix it names is
  `notion-track init --me <name>`, which moves it.
- When the identity comes from `credentials.yml` or the environment, there is
  nothing to warn about and the check reports `ok` naming the source.

`doctor` is where a stale setup is meant to surface, so it is also what tells
a user with an old `config.yml` that their identity is in the old place.

### 3.7 Migration is read-only

Nothing rewrites `config.yml`. A file that is meant to be committed must not
be edited as a side effect of an unrelated command — the diff would appear in
someone's `git status` with no explanation, and in the worst case get
committed, which is the exact failure this design exists to prevent.

The migration path is: the legacy field keeps working, `doctor` says where it
is and how to move it, and `init --me` moves it. Removing `me:` from
`config.yml` afterwards is the user's edit to make.

## 4. Non-goals

- **Deriving the identity from the token.** `/v1/users/me` returns the *bot*
  for an internal integration, not the human operating it —
  `notion.Client.Me` already does this and `doctor` uses it as the token's
  own name. There is no API path from an integration token to the person
  holding it.
- **Writing to the user's shell profile.** `init` names the `export` line for
  the token and refuses to edit `.zshrc`; the identity gets the same
  treatment. After this change nobody needs the line anyway.
- **Removing `Profile.Me`.** It stays readable indefinitely. A CI profile
  whose "identity" is deliberately shared — a service account — is a
  legitimate use of a shared file.
- **Encrypting or otherwise protecting `credentials.yml`.** Out of scope and
  unchanged by this work.

## 5. Documentation

Every surface that currently teaches "put it in the environment variable"
has to teach the new default instead. This is not an afterthought to the
change; it is most of its user-visible value.

- **`README.md` / `README.it.md`** — the Environment variables table, the
  identity paragraph under `--assignee`, the `config.yml` example carrying
  `me:`, the `init --me` description, the `doctor` assignee description, and
  the quick-start step that tells the reader to export `NOTION_TRACK_ME`.
  Both languages, kept in step.
- **`skills/notion-track/SKILL.md`** — line 143-146 defines `me` as
  "`NOTION_TRACK_ME`, or the profile's `me:`". This is the agent-facing
  contract and must state the new precedence. This is the file that matters
  most for the user's actual workflow: it is what an agent reads.
- **`skills/notion-track/README.md`** — checked and updated if it repeats the
  claim.

**Out of reach:** this repository has no `CLAUDE.md`. The one the user's
other agent edited belongs to the project where notion-track is *used*, which
is not this working tree. It cannot be updated from here, and the plan must
say so rather than silently omitting it.

## 6. Verifiable requirements

Each of these is a statement a test or a `grep` can settle:

1. `grep -n "me:" ` over a `config.yml` written by `init --me` returns
   nothing — the identity is not in the shared file.
2. A `credentials.yml` written by `init --me` contains
   `identities:\n  <profile>: <canonical name>`.
3. With all three sources set, `--assignee me` resolves to the environment
   variable's value.
4. With the environment unset and both files set, it resolves to
   `credentials.yml`.
5. With only `config.yml`'s `me:` set, it still resolves — and `doctor`
   reports a `warn` naming `init --me` as the fix.
6. `internal/cli/init.go` no longer contains the string "meant to be shared".
7. A `credentials.yml` containing `identities` loads without error in a
   binary whose `Credentials` struct lacks the field (the compatibility claim
   in §3.2, testable by decoding into the old shape).
8. The wizard's identity step does not appear when the assignee role is
   unmapped.

## 7. Risks

- **Two write paths to `credentials.yml`.** `SaveToken` currently writes the
  whole struct (`internal/config/config.go:307` constructs a fresh
  `Credentials`). Saving an identity must not clobber the token, and saving a
  token must not clobber identities. Whichever function writes must read the
  existing file first and merge. This is the single most likely defect in the
  change, and it destroys a working setup when it happens.
- **Atomicity.** `SaveToken` writes atomically via a temp file; the identity
  path must use the same mechanism rather than a second, weaker one.
- **The wizard's optional step.** A step that cannot be skipped would make
  identity effectively mandatory and break the "no assignee column" case.
