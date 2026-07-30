# Identity in credentials.yml — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the `--assignee me` identity out of the shared `config.yml` into the per-user `credentials.yml`, so the four warnings that compensate for its current location can be deleted.

**Architecture:** `credentials.yml` gains an `identities` map keyed by profile name. A single resolver in `internal/config` owns the precedence `NOTION_TRACK_ME` → `credentials.yml` → `config.yml`'s legacy `me:`. The CLI applies the resolved value to the profile it hands the service, so `internal/service` keeps reading `profile.Me` unchanged.

**Tech Stack:** Go 1.26, cobra, `gopkg.in/yaml.v3`, bubbletea (TUI), `net/http/httptest` (fakes).

**Spec:** `docs/superpowers/specs/2026-07-30-notion-track-identity-in-credentials-design.md`

## Global Constraints

- `CurrentSchemaVersion` stays at **1**. Do not bump it. See spec §3.2.
- Nothing may rewrite `config.yml` as a side effect. Migration is read-only. Spec §3.7.
- `Profile.Me` stays readable forever as the legacy source. Do not delete the field.
- Writing an identity must not clobber the token, and writing a token must not clobber identities. Spec §7.
- Every write to `credentials.yml` uses the existing atomic temp-file-then-rename mechanism and `0600`. Never add a second, weaker write path.
- **Any test that reads or writes `credentials.yml` must isolate the real one first.** In `internal/cli` that is `withIsolatedUserConfigDir(t)` (`internal/cli/get_test.go:175`, which repoints `XDG_CONFIG_HOME`/`HOME`/`AppData` at a temp dir); in `internal/config` it is `withTempCredentials(t)`. Without it, `config.SaveIdentity` resolves `os.UserConfigDir()` and **writes into the developer's own `credentials.yml`**. Neither `stubForAssignee` (`internal/cli/upsert_test.go:151`) nor the wizard tests isolate today, because nothing they touched ever read that file — copying their shape without adding the helper is the mistake this constraint exists to prevent.
- **Any test involving the identity must also do `t.Setenv(config.MeEnv, "")`.** Whoever runs the suite may well have `NOTION_TRACK_ME` exported — the current README tells them to. The package already guards this way at `internal/cli/upsert_test.go:146-150`, `internal/service/doctor_test.go:289-291` and `internal/config/config_test.go:453-456`; follow it.
- Never echo a token in output or in an error. The existing comments at `internal/config/config.go:265-273` explain why `yaml` errors from this file must not be wrapped with `%w`.
- Both READMEs (`README.md`, `README.it.md`) change together and must stay in step.
- Run the full local gate before every push: `gofmt -l .` (must be empty), `go vet ./...`, `go build ./...`, `go test ./... -race`, `staticcheck ./...`. `gofmt` and `staticcheck` are NOT run by `go test`/`go vet`.
- Conventional-commit subjects. **Never** add a `Co-Authored-By` trailer.

---

### Task 1: `Identities` on the credentials struct, and a write that merges

**Files:**
- Modify: `internal/config/config.go:203-206` (the `Credentials` struct), `internal/config/config.go:297-347` (`SaveToken`)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `Credentials.Identities map[string]string`; `config.SaveIdentity(profile, name string) error`; an unexported `writeCredentials(Credentials) error` both savers call.

**Why this is first and why it is dangerous:** `SaveToken` today builds a fresh struct — `creds := Credentials{SchemaVersion: CurrentSchemaVersion, Token: token}` — and writes it. The moment `Identities` exists, that line silently deletes every identity whenever a token is saved. This task removes that hazard before anything can depend on it.

- [ ] **Step 1: Write the failing tests**

In `internal/config/config_test.go`. Use the existing pattern for pointing `credentialsPath` at a temp dir — find it in the file and follow it rather than inventing a new one.

```go
func TestSaveIdentityKeepsTheToken(t *testing.T) {
	withTempCredentials(t)

	if err := SaveToken("secret_abc"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	if err := SaveIdentity("default", "Jordan Lee"); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	token, source, err := LoadToken()
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if token != "secret_abc" || source != "file" {
		t.Errorf("LoadToken() = %q, %q; want the token saved before the identity", token, source)
	}
}

func TestSaveTokenKeepsIdentities(t *testing.T) {
	withTempCredentials(t)

	if err := SaveIdentity("work", "Jordan Lee"); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}
	if err := SaveToken("secret_abc"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	creds, err := loadCredentials()
	if err != nil {
		t.Fatalf("loadCredentials: %v", err)
	}
	if got := creds.Identities["work"]; got != "Jordan Lee" {
		t.Errorf("identities[work] = %q after SaveToken; want it preserved", got)
	}
}

func TestSaveIdentityOverwritesTheSameProfileOnly(t *testing.T) {
	withTempCredentials(t)

	if err := SaveIdentity("default", "Old Name"); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}
	if err := SaveIdentity("work", "Jordan Lee"); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}
	if err := SaveIdentity("default", "New Name"); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	creds, err := loadCredentials()
	if err != nil {
		t.Fatalf("loadCredentials: %v", err)
	}
	if creds.Identities["default"] != "New Name" || creds.Identities["work"] != "Jordan Lee" {
		t.Errorf("identities = %v; want default replaced and work untouched", creds.Identities)
	}
}

// A credentials.yml written by this binary must stay readable by one built
// before Identities existed. This is the compatibility claim in spec §3.2,
// and the reason CurrentSchemaVersion is not bumped.
func TestIdentitiesAreIgnoredByAnOlderStruct(t *testing.T) {
	withTempCredentials(t)

	if err := SaveToken("secret_abc"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	if err := SaveIdentity("default", "Jordan Lee"); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	path, err := credentialsPath()
	if err != nil {
		t.Fatalf("credentialsPath: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading credentials: %v", err)
	}

	// The shape as it was before this change.
	var old struct {
		SchemaVersion int    `yaml:"schema_version"`
		Token         string `yaml:"token"`
	}
	if err := yaml.Unmarshal(raw, &old); err != nil {
		t.Fatalf("an older binary cannot read this file: %v", err)
	}
	if old.Token != "secret_abc" {
		t.Errorf("old.Token = %q; want the token still readable", old.Token)
	}
}
```

`withTempCredentials` already exists in `internal/config/config_test.go:126` and already clears `TokenEnv` — use it, do not write a second one.

`TestIdentitiesAreIgnoredByAnOlderStruct` needs `gopkg.in/yaml.v3`, which `config_test.go` does not currently import. Add it.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/config/ -run 'TestSaveIdentity|TestSaveTokenKeepsIdentities|TestIdentitiesAreIgnored' -v`
Expected: FAIL — `undefined: SaveIdentity`, `undefined: loadCredentials`.

A build failure here is the expected first state, but do not accept a build failure as evidence for the *next* step's assertions.

- [ ] **Step 3: Add the field**

```go
type Credentials struct {
	SchemaVersion int    `yaml:"schema_version"`
	Token         string `yaml:"token"`
	// Identities maps a profile name to the value "--assignee me" resolves
	// to for this user. It lives here rather than in config.yml for the same
	// reason the token does: it is personal, and config.yml is meant to be
	// committed. Keyed by profile because two profiles can point at
	// workspaces that spell the same person differently.
	Identities map[string]string `yaml:"identities,omitempty"`
}
```

- [ ] **Step 4: Extract loading and writing, so both savers share one path**

Add `loadCredentials`, which is `LoadToken`'s file half with the token-specific parts removed. Keep every existing comment about not wrapping the yaml error — the reasoning is unchanged.

```go
// loadCredentials reads credentials.yml whole. A missing file is not an
// error: it is the ordinary state before init has ever run, and both callers
// treat it as an empty set.
func loadCredentials() (Credentials, error) {
	path, err := credentialsPath()
	if err != nil {
		return Credentials{}, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Credentials{}, nil
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("config: %w: %v", ErrCredentialsUnreadable, err)
	}
	var creds Credentials
	if err := yaml.Unmarshal(raw, &creds); err != nil {
		// See the note in LoadToken: this file's scalars are secrets, so the
		// underlying yaml error must never reach the caller.
		return Credentials{}, fmt.Errorf("config: %s: %w", path, ErrInvalidCredentials)
	}
	return creds, nil
}
```

Move the whole body of `SaveToken` from `path, err := credentialsPath()` to the final `return nil` into `writeCredentials(creds Credentials) error`, replacing the `creds := Credentials{...}` line with the parameter. Keep every comment — the CreateTemp reasoning and the `.tmp` leftover removal are load-bearing and must not be summarised away.

Then both savers become read-modify-write:

```go
func SaveToken(token string) error {
	creds, err := loadCredentials()
	if err != nil {
		return err
	}
	creds.SchemaVersion = CurrentSchemaVersion
	creds.Token = token
	return writeCredentials(creds)
}

// SaveIdentity records the value "--assignee me" resolves to for one profile.
// It reads the file first so that saving an identity cannot discard the token
// sitting next to it — the two are written by different commands at different
// times, and a blind write would destroy a working setup.
func SaveIdentity(profile, name string) error {
	creds, err := loadCredentials()
	if err != nil {
		return err
	}
	creds.SchemaVersion = CurrentSchemaVersion
	if creds.Identities == nil {
		creds.Identities = map[string]string{}
	}
	creds.Identities[profile] = name
	return writeCredentials(creds)
}
```

`LoadToken` keeps its env-first shortcut and then delegates to `loadCredentials`; do not duplicate the read.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/config/ -run 'TestSaveIdentity|TestSaveTokenKeepsIdentities|TestIdentitiesAreIgnored' -v`
Expected: PASS.

- [ ] **Step 6: Prove the merge test bites**

Temporarily change `SaveIdentity` to `creds := Credentials{SchemaVersion: CurrentSchemaVersion, Identities: map[string]string{profile: name}}` — the blind write this task exists to prevent.

Run: `go test ./internal/config/ -run TestSaveIdentityKeepsTheToken -v`
Expected: FAIL, with the token empty.

This must be a real assertion failure, not a build error. Revert the mutation before continuing.

- [ ] **Step 7: Run the whole package and commit**

Run: `go test ./internal/config/ -race`
Expected: PASS.

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): store the personal identity in credentials.yml"
```

---

### Task 2: One resolver owning the precedence

**Files:**
- Modify: `internal/config/config.go:178-180` (remove the `MeEnv` override from `Resolve`), `internal/config/config.go:59-71` (`Profile`)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `loadCredentials` from Task 1.
- Produces: `config.ResolveIdentity(profileName string, p Profile) (value string, source string, err error)` where `source` is one of `"env"`, `"file"`, `"legacy"`, or `""` when nothing is set; `Profile.MeSource string` (not serialised); `(*Config).ProfileName(requested string) string`.

**`ProfileName` is not optional.** The identity is keyed by the profile name, and the name the caller passes is only the `--profile` flag: `NOTION_TRACK_PROFILE` and `default_profile` are applied *inside* `Resolve` (`internal/config/config.go:154-159`). Task 3 needs that resolved name, and must not recompute the chain. Extract it:

```go
// ProfileName applies the profile-selection precedence — the requested name,
// then NOTION_TRACK_PROFILE, then default_profile — and returns the name that
// wins. Resolve uses it, and so does anything else that needs to know which
// profile a run is actually about (the identity is keyed by that name).
func (c *Config) ProfileName(requested string) string {
	if requested == "" {
		requested = os.Getenv(ProfileEnv)
	}
	if requested == "" {
		requested = c.DefaultProfile
	}
	return requested
}
```

and make `Resolve` call it instead of repeating those two `if` blocks.

**Why the override moves:** leaving `MeEnv` applied inside `Resolve` and adding a second source elsewhere puts the precedence chain in two files. The next person to add a source will update one of them.

- [ ] **Step 1: Write the failing tests**

Every one of these calls `withTempCredentials(t)`, and every one that is *not* about the environment must also do `t.Setenv(MeEnv, "")` — `withTempCredentials` clears only `TokenEnv`, so a developer with `NOTION_TRACK_ME` exported would otherwise see three of these four fail against a correct implementation.

```go
func TestResolveIdentityPrefersTheEnvironment(t *testing.T) {
	withTempCredentials(t)
	if err := SaveIdentity("default", "From File"); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}
	t.Setenv(MeEnv, "From Env")

	got, source, err := ResolveIdentity("default", Profile{Me: "From Legacy"})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if got != "From Env" || source != "env" {
		t.Errorf("ResolveIdentity() = %q, %q; want the environment to win", got, source)
	}
}

func TestResolveIdentityPrefersTheFileOverTheLegacyField(t *testing.T) {
	withTempCredentials(t)
	t.Setenv(MeEnv, "")
	if err := SaveIdentity("default", "From File"); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	got, source, err := ResolveIdentity("default", Profile{Me: "From Legacy"})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if got != "From File" || source != "file" {
		t.Errorf("ResolveIdentity() = %q, %q; want credentials.yml to win over me:", got, source)
	}
}

// The whole point of keeping Profile.Me: an existing config keeps working.
func TestResolveIdentityFallsBackToTheLegacyField(t *testing.T) {
	withTempCredentials(t)
	t.Setenv(MeEnv, "")

	got, source, err := ResolveIdentity("default", Profile{Me: "From Legacy"})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if got != "From Legacy" || source != "legacy" {
		t.Errorf("ResolveIdentity() = %q, %q; want the config field as the last resort", got, source)
	}
}

func TestResolveIdentityIsKeyedByProfile(t *testing.T) {
	withTempCredentials(t)
	t.Setenv(MeEnv, "")
	if err := SaveIdentity("work", "Jordan Lee"); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	got, source, err := ResolveIdentity("default", Profile{})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if got != "" || source != "" {
		t.Errorf("ResolveIdentity(default) = %q, %q; want nothing — the identity belongs to another profile", got, source)
	}
}
```

- [ ] **Step 2: Run and watch it fail**

Run: `go test ./internal/config/ -run TestResolveIdentity -v`
Expected: FAIL — `undefined: ResolveIdentity`.

- [ ] **Step 3: Add the field and the resolver**

On `Profile`:

```go
	// MeSource records where the effective identity came from: "env",
	// "file", "legacy", or "" when there is none. Populated by
	// ResolveIdentity and never persisted — it describes this run, not the
	// configuration. doctor reads it to tell a user whose identity is still
	// in the shared config file how to move it.
	MeSource string `yaml:"-"`
```

```go
// ResolveIdentity answers who "--assignee me" means, in one place:
//
//	NOTION_TRACK_ME  →  credentials.yml identities[profile]  →  the profile's me:
//
// The environment wins because CI passes an identity that must never be read
// off disk. credentials.yml comes next because it is per-user. The profile's
// me: is last and exists only so that configurations written before the
// identity moved keep working — source "legacy" is what lets doctor say so.
func ResolveIdentity(profileName string, p Profile) (value string, source string, err error) {
	if v := os.Getenv(MeEnv); v != "" {
		return v, "env", nil
	}
	creds, err := loadCredentials()
	if err != nil {
		return "", "", err
	}
	if v := creds.Identities[profileName]; v != "" {
		return v, "file", nil
	}
	if p.Me != "" {
		return p.Me, "legacy", nil
	}
	return "", "", nil
}
```

Delete these three lines from `Resolve`:

```go
	if v := os.Getenv(MeEnv); v != "" {
		p.Me = v
	}
```

Update `MeEnv`'s doc comment at `internal/config/config.go:31-36`: it currently explains that the variable exists because config.yml is shared. That reason is now handled by the file split; the comment should say the variable is the override, and point at `ResolveIdentity` for the order.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/config/ -run TestResolveIdentity -v`
Expected: PASS.

- [ ] **Step 5: Find every caller the removed override affected**

Run: `grep -rn "MeEnv\|\.Me\b" --include="*.go" internal/ | grep -v _test.go`

Every non-test reader of `profile.Me` must now receive a profile whose `Me` was set by `ResolveIdentity`. Task 3 wires that. Note in the task report which call sites you found — the reviewer needs it.

- [ ] **Step 6: Run the package and commit**

Run: `go test ./internal/config/ -race`
Expected: PASS. Any existing test asserting that `Resolve` applies `NOTION_TRACK_ME` will now fail — that behaviour moved, so move the assertion to `ResolveIdentity` rather than deleting it.

One thing to know rather than fix: between this commit and Task 3's, `NOTION_TRACK_ME` does nothing for any command, because `Resolve` has stopped applying it and no caller resolves it yet. No test goes red in the gap (the CLI tests only ever *clear* that variable), so nothing will tell you. It is a bisect hazard, not a defect — do not "fix" it by leaving the override in `Resolve`, which would put the precedence back in two places.

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): resolve the identity in one place, with a source"
```

---

### Task 3: Wire the resolved identity into every command

**Files:**
- Modify: `internal/cli/context.go`, in `buildService` — the single seam every command goes through (`get`, `set`, `list`, `upsert`, `apply`, `browse`, `doctor`, `mcp`), at `internal/cli/context.go:44-45`
- Test: `internal/cli/` (the package's existing command tests)

**Interfaces:**
- Consumes: `config.ResolveIdentity` from Task 2.
- Produces: every command's profile carries the effective `Me` and `MeSource`.

**Why:** `internal/service` reads `s.profile.Me` (`internal/service/service.go:370-374`) and must keep doing so — this change is about where the value comes from, not how it is used. Assigning the resolved value onto the profile before the service is built keeps `internal/service` untouched except for doctor's message.

- [ ] **Step 1: Write the failing test**

In the CLI package, using its existing stubbed-API helper. Assert the end-to-end behaviour, not the plumbing: `--assignee me` with the identity **only** in `credentials.yml` must send that name to the API.

```go
func TestAssigneeMeUsesTheIdentityFromCredentials(t *testing.T) {
	// withIsolatedUserConfigDir(t) FIRST — see the global constraint. Without
	// it this test writes into the developer's own credentials.yml.
	// t.Setenv(config.MeEnv, "") as well.
	//
	// Follow stubForAssignee (internal/cli/upsert_test.go:151) for the stubbed
	// API and temp config; capture the PATCH body so the assertion is about
	// what was actually sent.
	//
	// The written profile must NOT carry me:.
	// Save the identity with config.SaveIdentity(<the profile the fixture
	// uses>, "Jordan Lee") — the fixtures set default_profile to "work", so
	// that is the key, and the command must be run WITHOUT --profile. That
	// combination is the whole point: it is what proves the resolved name is
	// used and not the raw flag.
	// Then run: set --ticket TASK-1 --status Done --assignee me
	// Assert the captured body assigns "Jordan Lee".
}
```

Write it out fully against the helpers that exist in the package. Read `stubForAssignee` and a neighbouring `me` test (`TestSetMeUsesTheConfiguredIdentity`) first and match their shape.

- [ ] **Step 2: Run and watch it fail**

Run: `go test ./internal/cli/ -run TestAssigneeMeUsesTheIdentityFromCredentials -v`
Expected: FAIL — the identity is not found, so `--assignee me` returns `ErrNoIdentity`.

Confirm the failure message is that one. A failure for any other reason means the test's setup is wrong, not the feature.

- [ ] **Step 3: Apply the resolver at the seam**

Immediately after the profile is resolved, before the service is constructed:

In `buildService`, replacing `internal/cli/context.go:44-45`:

```go
	requested, _ := cmd.Flags().GetString("profile")
	// The resolved name, not the flag: NOTION_TRACK_PROFILE and
	// default_profile are applied inside Resolve, and the identity is keyed
	// by the profile the run is actually about.
	name := cfg.ProfileName(requested)
	profile, err := cfg.Resolve(name)
	if err != nil {
		return nil, Errorf(ExitUsage, "%v", err)
	}

	me, source, err := config.ResolveIdentity(name, profile)
	if err != nil {
		return nil, err
	}
	profile.Me, profile.MeSource = me, source
```

Passing the already-resolved `name` back into `Resolve` is harmless — `ProfileName` is idempotent, and a non-empty name skips both fallbacks.

**Do not** write `config.ResolveIdentity(requested, profile)`. `requested` is the flag value alone, so for every user who omits `--profile` — the common case — it looks up `identities[""]` and silently finds nothing.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/cli/ -run 'TestAssigneeMeUsesTheIdentityFromCredentials|TestSetMe|TestAssignee' -v`
Expected: PASS. Check the output lists `--- PASS` lines for named tests: a `-run` regex that matches nothing makes `go test` print a bare `ok` having run zero tests, which reads exactly like success.

- [ ] **Step 5: Run everything and commit**

Run: `go test ./... -race`
Expected: PASS.

```bash
git add -A
git commit -m "feat(cli): resolve the identity for every command"
```

---

### Task 4: `init --me` writes to the per-user file

**Files:**
- Modify: `internal/cli/init.go:336-362`
- Test: `internal/cli/init_test.go`

**Interfaces:**
- Consumes: `config.SaveIdentity` from Task 1.
- Produces: nothing new.

**The trap in this task:** the identity must be keyed by the profile name `saveInitProfile` actually writes to — `internal/cli/init.go:240-243`, which reads the `--profile` flag and falls back to the literal `"default"`. It is **not** the resolved `default_profile`. A test that omits `--profile` while the config file names a different default will pass or fail for the wrong reason; `init_test.go` has an `initArgs` helper (see its comment around `init_test.go:458`) that exists precisely because of this. Use it.

- [ ] **Step 1: Write the failing tests**

```go
func TestInitMeWritesToCredentialsNotConfig(t *testing.T) {
	// withIsolatedUserConfigDir(t) and t.Setenv(config.MeEnv, "") first.
	// withStubbedAPI + initArgs, as the neighbouring init tests do.
	// Run: init ... --assignee-prop Owner --me "jordan"
	// Assert:
	//   1. the written config.yml profile has Me == "" — grep the raw bytes
	//      for "me:" as well, so a future field rename cannot hide a regression
	//   2. credentials.yml identities[<the profile initArgs used>] == the
	//      canonical option spelling ("Jordan Lee", not "jordan")
}

func TestInitMePrintsNoSharedConfigWarning(t *testing.T) {
	// Same setup.
	// Assert stderr does NOT contain "meant to be shared".
	// Assert STDOUT names the credentials file. The confirmation is printed
	// with cmd.Printf, and the root command sends Out to stdout
	// (internal/cli/cli.go:74-75) — the token-save confirmation is asserted
	// the same way, via captureStdout (internal/cli/init_test.go:102-137).
	// Asserting the path on stderr fails a correct implementation.
}
```

**Two existing tests assert the behaviour this task removes, and both must be updated in this task — they are not regressions:**

- `internal/cli/init_test.go:579` `TestInitMeStoresTheCanonicalValue` asserts `writtenProfile(t, cfg).Me` holds the canonical spelling. The canonical-spelling guarantee survives; its location moves. Rewrite the assertion to read `credentials.yml` instead of the profile.
- `internal/cli/init_test.go:607` `TestInitMeWarnsThatTheConfigIsShared` asserts the deleted warning is printed. Delete it — `TestInitMePrintsNoSharedConfigWarning` above is its replacement, asserting the opposite.

- [ ] **Step 2: Run and watch them fail**

Run: `go test ./internal/cli/ -run 'TestInitMe' -v`
Expected: FAIL — the identity is still written into the profile and the warning is still printed.

- [ ] **Step 3: Rewrite the block**

Replace `internal/cli/init.go:336-362` with:

```go
			resolvedMe := ""
			if me != "" {
				if assigneeProp == "" {
					return Errorf(ExitUsage,
						"--me needs an assignee column to resolve against\n"+
							"  fix: pass --assignee-prop <name> as well")
				}
				resolvedMe, err = tracker.ResolveOption("me", me, schema.Properties[assigneeProp].Options)
				if err != nil {
					return Errorf(ExitUsage, "%v", err)
				}
			}

			if err := saveInitProfile(cmd, config.Profile{
				DatabaseID:   databaseID,
				DataSourceID: dataSourceID,
				StatusType:   statusType,
				Properties:   props,
			}, schema.Title); err != nil {
				return err
			}

			// The identity goes in credentials.yml, not in the profile above:
			// config.yml is committed and shared, and an identity written
			// there would be everyone's. Saved after the profile so that a
			// failure to write it leaves a usable configuration behind rather
			// than an identity pointing at a profile that does not exist.
			if resolvedMe != "" {
				name, _ := cmd.Flags().GetString("profile")
				if name == "" {
					name = "default"
				}
				if err := config.SaveIdentity(name, resolvedMe); err != nil {
					return err
				}
				credPath, err := config.CredentialsPath()
				if err != nil {
					return err
				}
				cmd.Printf("identity %q saved to %s\n", resolvedMe, credPath)
			}
			return nil
```

Note `Me:` is gone from the profile literal, and the warning with it.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/cli/ -run 'TestInit' -v`
Expected: PASS — including `TestInitMeStoresTheCanonicalValue` after you rewrite it, and with `TestInitMeWarnsThatTheConfigIsShared` gone. If either still asserts the old location, fix the test: this task deliberately changes that behaviour.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/init.go internal/cli/init_test.go
git commit -m "feat(init): save the identity to credentials.yml and drop the warning"
```

---

### Task 5: The wizard asks for an identity

**Files:**
- Modify: `internal/tui/wizard.go`
- Test: `internal/tui/wizard_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `tui.Result` carries the typed identity; the exact field name you add is what Task 6 consumes — state it in your report.

**Constraints from the spec (§3.5):** the step appears **only** when the assignee role is mapped, and skipping it is a normal outcome, not an error.

- [ ] **Step 1: Read the model first**

The wizard is a state machine over `roles` with stages. Read `internal/tui/wizard.go` end to end before adding anything. An identity is **not** a role — roles map columns, and this maps a value. Do not add it to the `roles` slice; the `roleValue`/`setRole` switches are for columns, and the id role's own history (a `case` missing from both switches made its key a silent no-op) is the warning here.

Three facts about this wizard that contradict the obvious approach:

1. **There is no text input.** `wizard.go` imports `bubbles/list`, `tea` and `lipgloss` — nothing else. `textinput` exists only in `internal/tui/browse.go`. The identity step is a **picker over the assignee column's options**, which also makes the typo-cannot-reach-disk guarantee free (spec §3.5).
2. **There is no gap between "role selection" and "confirmation".** Roles are chosen *from* the confirmation screen: `updateConfirm` (`internal/tui/wizard.go:238`) loops into `stageEditRole` and back. The only correct insertion point is inside `updateConfirm`'s `"enter"` case (`wizard.go:243-251`), where `m.stage = stageDone; return m, tea.Quit` runs today — divert to the identity stage there when the assignee role is mapped and the identity has not been asked yet, and let that stage's own completion set `stageDone`.
3. The model must be able to reach the assignee column's options. Check what the model already holds; if it has the schema, read the options from it — do not thread a new parameter through if one is already there.

- [ ] **Step 2: Write the failing tests**

```go
func TestWizardSkipsTheIdentityStepWhenAssigneeIsUnmapped(t *testing.T) {
	// Drive the model to the point after role selection with no assignee
	// mapped, and assert the identity screen is never reached and Result
	// carries an empty identity.
}

func TestWizardCollectsTheIdentityWhenAssigneeIsMapped(t *testing.T) {
	// Map the assignee role, press enter on the confirm screen, pick an
	// option from the identity list; assert Result carries that option.
}

func TestWizardIdentityCanBeSkipped(t *testing.T) {
	// Map the assignee role, skip the step; assert Result carries "" and the
	// wizard completes successfully.
}
```

Match the existing tests' way of driving the model (they send `tea.KeyMsg` values directly — follow that, do not start a program).

- [ ] **Step 3: Run and watch them fail**

Run: `go test ./internal/tui/ -run 'TestWizard.*Identity' -v`

Quote the regex. Unquoted, zsh — this repo's shell — tries to glob `TestWizard.*Identity`, finds no matching file, and aborts before `go test` runs at all.

Expected: FAIL. Verify the failure is an assertion, not a compile error from a helper you have not written yet.

- [ ] **Step 4: Implement the step**

Add a `stageIdentity`, entered from `updateConfirm`'s `"enter"` case when the assignee role is mapped and the identity has not been asked yet, and leaving to `stageDone` when the user picks or skips. Build its list with the same `newList` helper the role screens use — the wizard has no other input mechanism, and adding one would be the largest change in this plan for the smallest reason.

Include an explicit skip entry in the list (or honour `esc`, if that reads better against the rest of the wizard's key handling): skipping must be reachable, and must complete the wizard rather than cancel it.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/tui/ -race`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/wizard.go internal/tui/wizard_test.go
git commit -m "feat(tui): ask for the identity when an assignee column is mapped"
```

---

### Task 6: The wizard's identity reaches credentials.yml

**Files:**
- Modify: `internal/cli/init.go` (the wizard branch, around `internal/cli/init.go:215`)
- Test: `internal/cli/init_test.go`

**Interfaces:**
- Consumes: the `tui.Result` field from Task 5 and `config.SaveIdentity` from Task 1.

**Note:** the wizard is driven through the `runWizard` seam (`internal/cli/init.go:157`), which tests replace. Stub it to return a `Result` carrying an identity — do not try to drive a real terminal.

- [ ] **Step 1: Write the failing test**

```go
func TestInitWizardSavesTheIdentity(t *testing.T) {
	// withIsolatedUserConfigDir(t) and t.Setenv(config.MeEnv, "") first — the
	// existing wizard tests (internal/cli/init_test.go:314-402) do neither,
	// because nothing they touched ever read credentials.yml. Copying them
	// without adding these two lines writes into the developer's real
	// credentials file.
	//
	// Replace runWizard (via the withFakeWizard helper) with a stub returning
	// a Result that maps an assignee column and carries an identity. Assert
	// credentials.yml holds it under the profile the wizard branch writes,
	// and that config.yml does not.
}
```

- [ ] **Step 2: Run and watch it fail**

Run: `go test ./internal/cli/ -run TestInitWizardSavesTheIdentity -v`
Expected: FAIL — nothing is written.

- [ ] **Step 3: Save it, using the same helper Task 4 added**

If Task 4's save block is more than a few lines, extract it to a small function and call it from both branches rather than duplicating it. Both branches must agree on the profile name rule (`--profile`, else `"default"`).

- [ ] **Step 4: Run and commit**

Run: `go test ./internal/cli/ -race`
Expected: PASS.

```bash
git add internal/cli/init.go internal/cli/init_test.go
git commit -m "feat(init): persist the identity the wizard collected"
```

---

### Task 7: `doctor` reports the source and flags the legacy one

**Files:**
- Modify: `internal/service/doctor.go:152-180`
- Test: `internal/service/doctor_test.go`

**Interfaces:**
- Consumes: `Profile.MeSource` from Task 2.

- [ ] **Step 1: Write the failing tests**

```go
func TestDoctorWarnsWhenTheIdentityIsStillInTheConfigFile(t *testing.T) {
	// profile with Me set and MeSource "legacy", assignee column present and
	// the identity resolvable.
	// Assert: status "warn", detail names "notion-track init --me".
}

func TestDoctorIsQuietWhenTheIdentityComesFromCredentials(t *testing.T) {
	// Same, MeSource "file".
	// Assert: status "ok", and the detail does not recommend anything.
}

func TestDoctorIsQuietWhenTheIdentityComesFromTheEnvironment(t *testing.T) {
	// Same, MeSource "env". Assert: status "ok".
}
```

**Two existing tests build the profile directly and never set `MeSource`, so this task must update them:**

- `internal/service/doctor_test.go:377` `TestDoctorWarnsWhenTheIdentityLivesOnlyInTheSharedConfig` expects a `warn`. With `MeSource` unset it now gets `ok`. Set `MeSource: "legacy"` on its profile — which is what that test was always about.
- `internal/service/doctor_test.go:308` `TestDoctorAcceptsAnIdentityFromTheEnvironment` asserts the detail contains the **resolved canonical** option, not the raw configured value. Set `MeSource: "env"` on its profile, and keep the ok-message printing `resolved` (see Step 3) so the assertion stays true.

- [ ] **Step 2: Run and watch them fail**

Run: `go test ./internal/service/ -run 'TestDoctor' -v`

Quote the regex — unquoted, zsh globs it and aborts before `go test` runs. And check the output lists `--- PASS`/`--- FAIL` lines for your three tests: a regex matching nothing makes `go test` print a bare `ok` having run zero tests.

Expected: FAIL — your three new tests, plus the two existing ones named below once you have updated them.

- [ ] **Step 3: Rewrite the check's tail**

Keep the resolution check exactly as it is — a renamed option must still warn. Replace only the source-related message. The old warning recommended `NOTION_TRACK_ME` for any file-based identity; the new one fires only for `"legacy"` and names the command that moves it:

The current tail is at `internal/service/doctor.go:185-189`: a `warn` whenever the identity came from the file, then `Check{"assignee", "ok", "--assignee me resolves to " + resolved}`. Note it prints `resolved` — the canonical option — not `s.profile.Me`. Keep that: an existing test asserts it, and it is the more useful answer.

```go
	if s.profile.MeSource == "legacy" {
		return Check{"assignee", "warn", fmt.Sprintf(
			"--assignee me resolves to %s, from the config file, which is meant to be shared\n"+
				"  fix: rerun 'notion-track init --me %s' to move it to your credentials file",
			resolved, s.profile.Me)}
	}
	return Check{"assignee", "ok", "--assignee me resolves to " + resolved}
```

`s.profile.MeSource`, not `p.MeSource`: `checkAssignee` is a method on `*Service` (`internal/service/doctor.go:152`) and its `prop` variable is the column, not the profile.

Leaving the `ok` line unchanged is deliberate. A `doctor` that recites which file an identity came from every time it is correct adds noise to the common case; the source only matters when something needs doing about it.

- [ ] **Step 4: Run and commit**

Run: `go test ./internal/service/ -race`
Expected: PASS.

```bash
git add internal/service/doctor.go internal/service/doctor_test.go
git commit -m "feat(doctor): report where the identity came from"
```

---

### Task 8: Documentation — both READMEs

**Files:**
- Modify: `README.md`, `README.it.md`

**Every location, found by:** `grep -n "NOTION_TRACK_ME\|me:" README.md README.it.md`

At the time of writing that is: the quick-start export step (~line 119), the `--me` description (~187), the `--assignee me` example and the identity paragraph (~227-233), the `doctor` assignee description (~384), the `config.yml` example (~410-424), and the environment-variables table and precedence list (~442-451). Re-run the grep — line numbers drift.

- [ ] **Step 1: Rewrite them**

What must be true afterwards:

- The default path is `init --me <name>` (or the wizard), which writes to `credentials.yml`. `NOTION_TRACK_ME` is documented as the **override**, for CI and one-off runs — not as the thing users must set up.
- The quick-start no longer tells a reader to add an export to their shell profile.
- The `config.yml` example no longer shows `me:` as a normal field. Mention it once, as legacy that still works, where the precedence is explained.
- The precedence list reads `NOTION_TRACK_ME` → `credentials.yml` → `config.yml`'s `me:` (legacy).
- The `doctor` description matches Task 7's actual behaviour: `warn` only for the legacy source.
- Nothing claims `init --me` warns about the shared file.

Keep both languages in step: same structure, same examples, same line of argument.

- [ ] **Step 2: Verify no stale claim survives**

Run: `grep -n "meant to be shared\|export NOTION_TRACK_ME" README.md README.it.md`

Read every hit. A surviving mention is only correct if it is describing the legacy field or the CI override.

- [ ] **Step 3: Commit**

```bash
git add README.md README.it.md
git commit -m "docs: the identity now lives in the credentials file"
```

---

### Task 9: Documentation — the agent-facing skill

**Files:**
- Modify: `skills/notion-track/SKILL.md`
- Check, and modify if it repeats the claim: `skills/notion-track/README.md`

**Why this file matters most:** it is what an agent reads before touching a board. `SKILL.md:143-146` currently defines `me` as "`NOTION_TRACK_ME`, or the profile's `me:`" — after this change that is both incomplete and points at the discouraged source first.

- [ ] **Step 1: Update the `me` definition**

State the precedence in the same order the READMEs do, and say what an agent should tell a user who has no identity configured: `notion-track init --me "<name>"`, not "export a variable".

- [ ] **Step 2: Check the rest of the file**

Run: `grep -n "NOTION_TRACK_ME\|me:" skills/notion-track/SKILL.md skills/notion-track/README.md`

Fix every hit that is now wrong.

- [ ] **Step 3: Commit**

```bash
git add skills/notion-track/SKILL.md skills/notion-track/README.md
git commit -m "docs(skill): teach the agent where the identity lives"
```

---

### Task 10: The strings compiled into the binary

**Files:**
- Modify: `internal/service/service.go:55-57`, `internal/cli/upsert.go:37`, `internal/cli/list.go:88`
- Test: whichever package tests already assert on these strings — find them with `grep -rn "NOTION_TRACK_ME\|MeEnv" --include="*_test.go" internal/`

**Why this is a task and not a footnote:** these are what a user reads at the moment they get it wrong, which is more often than they read either README. They currently teach the old precedence.

- [ ] **Step 1: `ErrNoIdentity`**

`internal/service/service.go:55-57` currently offers `export NOTION_TRACK_ME=<name>` first and `notion-track init --me <name>` second. Swap the order and the emphasis: `init --me` is the fix, the environment variable is the override. Keep it a `var` built with `errors.New` — `internal/cli/output.go` maps it to `ExitUsage` with `errors.Is`, so it must stay a sentinel.

- [ ] **Step 2: The two flag help strings**

`internal/cli/upsert.go:37` and `internal/cli/list.go:88` both say `'me' stands for NOTION_TRACK_ME`. Flag help is one line and shares a screen with thirty others: say `'me' stands for your configured identity`, and let the error message and the READMEs carry the detail.

- [ ] **Step 3: Update any test asserting the old strings, then run**

Run: `go test ./... -race`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/service/service.go internal/cli/upsert.go internal/cli/list.go
git commit -m "docs(cli): the error and flag help name init --me, not the override"
```

---

### Task 11: Whole-branch verification

**Files:** none — this task changes nothing unless it finds something.

- [ ] **Step 1: The five gates**

```bash
gofmt -l .
go vet ./...
go build ./...
go test ./... -race
staticcheck ./...
```

Expected: `gofmt -l .` prints nothing; everything else passes.

- [ ] **Step 2: The spec's verifiable requirements**

Work through spec §6 one by one and record the evidence for each in the task report. Two of them are greps:

```bash
grep -rn "meant to be shared" internal/
grep -rn "NOTION_TRACK_ME" internal/
```

For the first: no hits in `internal/cli/init.go`. A hit in `doctor.go` is correct — that is Task 7's legacy warning.

For the second (spec §6.9): the only acceptable hits are the `MeEnv` constant's own definition and doc comment, `ResolveIdentity`, and tests. A hit in a user-facing string means Task 10 missed one.

- [ ] **Step 3: An end-to-end read of the diff**

`git diff main...HEAD` and read it whole. Specifically confirm: no `CurrentSchemaVersion` change, no code path that writes `config.yml` outside `saveInitProfile`, and no second write path to `credentials.yml`.

- [ ] **Step 4: Report**

Do not commit. Report what you verified and anything you could not.

---

## Out of scope, and why

**`CLAUDE.md`.** This repository has none. The `CLAUDE.md` the user's other agent edited belongs to the project where notion-track is *used*, which is not this working tree — it cannot be reached from here. After this branch merges, that file needs the same precedence update, and only the user can point at it.

**Removing `me:` from the user's own `config.yml`.** Read-only migration is a deliberate decision (spec §3.7): a command that edits a committed file as a side effect produces an unexplained diff in someone's `git status`.
