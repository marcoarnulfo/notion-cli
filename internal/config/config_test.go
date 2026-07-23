package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withTempConfig(t *testing.T) string {
	t.Helper()
	t.Setenv(ProfileEnv, "")
	t.Setenv(DatabaseEnv, "")
	t.Setenv(DataSourceEnv, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	old := configPath
	configPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { configPath = old })
	return path
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	withTempConfig(t)

	cfg := &Config{
		SchemaVersion:  CurrentSchemaVersion,
		DefaultProfile: "work",
		Profiles: map[string]Profile{
			"work": {
				DatabaseID:   "db1",
				DataSourceID: "ds1",
				StatusType:   "status",
				Properties:   Properties{Ticket: "Ticket", Status: "Stato", Title: "Name", Due: "Scadenza"},
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, err := got.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.DataSourceID != "ds1" || p.Properties.Status != "Stato" {
		t.Fatalf("profile = %+v", p)
	}
}

// The config holds no secret, but it sits next to one: 0600 keeps it boring.
func TestSaveUsesRestrictivePermissions(t *testing.T) {
	path := withTempConfig(t)
	cfg := &Config{SchemaVersion: CurrentSchemaVersion, DefaultProfile: "w",
		Profiles: map[string]Profile{"w": {DataSourceID: "ds"}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permissions = %o, want 600", perm)
	}
}

func TestResolveNamedProfile(t *testing.T) {
	t.Setenv(ProfileEnv, "")
	t.Setenv(DatabaseEnv, "")
	t.Setenv(DataSourceEnv, "")
	cfg := &Config{DefaultProfile: "work", Profiles: map[string]Profile{
		"work":     {DataSourceID: "ds1"},
		"personal": {DataSourceID: "ds2"},
	}}
	p, err := cfg.Resolve("personal")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.DataSourceID != "ds2" {
		t.Fatalf("data source = %q", p.DataSourceID)
	}
}

func TestResolveUnknownProfileListsAvailableOnes(t *testing.T) {
	t.Setenv(ProfileEnv, "")
	t.Setenv(DatabaseEnv, "")
	t.Setenv(DataSourceEnv, "")
	cfg := &Config{DefaultProfile: "work", Profiles: map[string]Profile{"work": {}}}
	_, err := cfg.Resolve("nope")
	if err == nil {
		t.Fatal("expected an error")
	}
	// The message must be actionable: an agent reading it should recover.
	if got := err.Error(); !strings.Contains(got, "work") {
		t.Fatalf("error %q does not list the available profiles", got)
	}
}

func TestTokenPrefersEnvAndFlagsItsOrigin(t *testing.T) {
	t.Setenv(TokenEnv, "ntn_from_env")
	tok, fromEnv := Token()
	if tok != "ntn_from_env" || !fromEnv {
		t.Fatalf("Token() = %q, %v", tok, fromEnv)
	}
}

func TestTokenAbsent(t *testing.T) {
	t.Setenv(TokenEnv, "")
	if tok, fromEnv := Token(); tok != "" || fromEnv {
		t.Fatalf("Token() = %q, %v", tok, fromEnv)
	}
}

// withTempCredentials points credentialsPath at a file in t.TempDir(), the
// same seam pattern withTempConfig uses for config.yml.
func withTempCredentials(t *testing.T) string {
	t.Helper()
	t.Setenv(TokenEnv, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.yml")
	old := credentialsPath
	credentialsPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { credentialsPath = old })
	return path
}

func TestLoadTokenPrefersEnvOverFile(t *testing.T) {
	path := withTempCredentials(t)
	if err := os.WriteFile(path, []byte("schema_version: 1\ntoken: ntn_from_file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(TokenEnv, "ntn_from_env")

	tok, source, err := LoadToken()
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if tok != "ntn_from_env" || source != "env" {
		t.Fatalf("LoadToken() = %q, %q, want ntn_from_env, env", tok, source)
	}
}

func TestLoadTokenFallsBackToFile(t *testing.T) {
	path := withTempCredentials(t)
	if err := os.WriteFile(path, []byte("schema_version: 1\ntoken: ntn_from_file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tok, source, err := LoadToken()
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if tok != "ntn_from_file" || source != "file" {
		t.Fatalf("LoadToken() = %q, %q, want ntn_from_file, file", tok, source)
	}
}

func TestLoadTokenAbsentEverywhere(t *testing.T) {
	withTempCredentials(t)

	tok, source, err := LoadToken()
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if tok != "" || source != "" {
		t.Fatalf("LoadToken() = %q, %q, want empty", tok, source)
	}
}

// The env var must never be persisted as a side effect of merely resolving
// it: a CI secret sitting in NOTION_TOKEN must not leave a trace on disk.
func TestLoadTokenNeverWritesAnEnvTokenToDisk(t *testing.T) {
	path := withTempCredentials(t)
	t.Setenv(TokenEnv, "ntn_from_env")

	if _, _, err := LoadToken(); err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credentials file was created for an env-sourced token: stat err = %v", err)
	}
}

func TestSaveTokenUsesRestrictivePermissions(t *testing.T) {
	path := withTempCredentials(t)

	if err := SaveToken("ntn_secret"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permissions = %o, want 600", perm)
	}
}

// os.WriteFile does not apply its mode argument to a file that already
// exists: it opens (without O_EXCL) and truncates, leaving whatever
// permissions the file already had. The temp file name SaveToken writes to
// is fixed and predictable (credentials.yml.tmp), so anything that leaves a
// residual temp file there — a crash between write and rename, a sync tool,
// a restore from backup, another process of the same user — can pre-create
// it wide open, and the secret then lands on disk exactly that way.
func TestSaveTokenFixesPermissionsEvenWhenATempFilePreExistsWideOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.yml")
	old := credentialsPath
	credentialsPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { credentialsPath = old })

	// Pre-create a permissive file at the exact temp name SaveToken used to
	// use, simulating a leftover from an interrupted previous run.
	if err := os.WriteFile(path+".tmp", []byte("stale"), 0o666); err != nil {
		t.Fatal(err)
	}

	if err := SaveToken("ntn_secret"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permissions = %o, want 600", perm)
	}
}

// yaml.v3's unmarshal error names the offending scalar (the whole value up
// to 10 characters, or the first 7 followed by "..." beyond that) — useful
// for an ordinary config file, but credentials.yml can hold nothing else
// but a secret, so any value that ends up in that error is a partial token
// leak. This reproduces the case that actually puts a token there: someone
// pastes their token into the wrong field, or the file otherwise gets out
// of sync so a string lands where an int is expected.
func TestLoadTokenParseErrorNeverContainsFileContent(t *testing.T) {
	path := withTempCredentials(t)
	secret := "ntn_supersecrettoken1234567890"
	body := "schema_version: " + secret + "\ntoken: abc\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := LoadToken()
	if err == nil {
		t.Fatal("expected an error for a malformed credentials file")
	}
	msg := err.Error()
	if strings.Contains(msg, secret) || strings.Contains(msg, secret[:7]) {
		t.Fatalf("error message leaks file content: %q", msg)
	}
}

// A credentials.yml that exists but can't be read (permission denied, or —
// as here, portably across who's running the test — a directory sitting
// where the file should be) used to produce a message that named the path
// twice: LoadToken's own "reading %s" wrapper, plus os.ReadFile's
// underlying *PathError, which already names the path itself.
func TestLoadTokenUnreadableFileErrorNamesThePathOnlyOnce(t *testing.T) {
	path := withTempCredentials(t)
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, err := LoadToken()
	if err == nil {
		t.Fatal("expected an error reading a directory as the credentials file")
	}
	msg := err.Error()
	if n := strings.Count(msg, path); n != 1 {
		t.Fatalf("error %q names the path %d times, want 1", msg, n)
	}
	if !errors.Is(err, ErrCredentialsUnreadable) {
		t.Fatalf("error %q does not wrap ErrCredentialsUnreadable", msg)
	}
}

// SaveToken moved from a fixed temp name (credentials.yml.tmp) to
// os.CreateTemp's random-suffixed one, so that fixed name is no longer read
// from or written to by anything — but a build from before that change
// could have left one behind, world-readable (WriteFile does not apply its
// mode to a file that already exists), sitting next to credentials.yml
// forever since nothing ever looks at it again. A successful SaveToken must
// clean it up.
func TestSaveTokenRemovesAStaleFixedNameTempFile(t *testing.T) {
	path := withTempCredentials(t)
	legacyTmp := path + ".tmp"
	if err := os.WriteFile(legacyTmp, []byte("token: ntn_old_leftover\n"), 0o666); err != nil {
		t.Fatal(err)
	}

	if err := SaveToken("ntn_secret"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	if _, err := os.Stat(legacyTmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale legacy temp file %s still present after a successful save (stat err = %v)", legacyTmp, err)
	}
}

// SaveToken's defer os.Remove(tmp) exists specifically so a failure between
// creating the temp file and the final rename never leaves a token sitting
// on disk under a throwaway name — a guarantee nothing had exercised.
// Renaming onto an existing directory is always an error (renaming a file
// over a directory is never allowed), which forces the failure without
// depending on any environment-specific permission behavior.
func TestSaveTokenLeavesNoResidualTempFileOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.yml")
	old := credentialsPath
	credentialsPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { credentialsPath = old })

	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := SaveToken("ntn_secret"); err == nil {
		t.Fatal("expected SaveToken to fail when the destination is a directory")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "credentials.yml" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("directory contains %v after a failed save, want only the pre-existing credentials.yml directory", names)
	}
}

func TestSaveTokenThenLoadTokenRoundTrips(t *testing.T) {
	withTempCredentials(t)

	if err := SaveToken("ntn_secret"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	tok, source, err := LoadToken()
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if tok != "ntn_secret" || source != "file" {
		t.Fatalf("LoadToken() = %q, %q, want ntn_secret, file", tok, source)
	}
}

// migrate had no test at all: emptying it left the whole suite green.
func TestLoadNormalisesAMissingSchemaVersion(t *testing.T) {
	path := withTempConfig(t)
	os.WriteFile(path, []byte("default_profile: work\nprofiles:\n  work:\n    data_source_id: ds1\n"), 0o600)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", cfg.SchemaVersion, CurrentSchemaVersion)
	}
}

// A config written by a newer build must still load: refusing to work would be
// worse than ignoring settings we do not understand.
func TestLoadAcceptsAFutureSchemaVersion(t *testing.T) {
	path := withTempConfig(t)
	body := fmt.Sprintf(
		"schema_version: %d\ndefault_profile: work\nprofiles:\n  work:\n    data_source_id: ds1\n",
		CurrentSchemaVersion+1)
	os.WriteFile(path, []byte(body), 0o600)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, err := cfg.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.DataSourceID != "ds1" {
		t.Fatalf("profile = %+v", p)
	}
}

func TestResolvePrecedence(t *testing.T) {
	withTempConfig(t)
	cfg := &Config{DefaultProfile: "work", Profiles: map[string]Profile{
		"work": {DataSourceID: "ds-work"},
		"env":  {DataSourceID: "ds-env"},
		"flag": {DataSourceID: "ds-flag"},
	}}

	t.Run("default profile when nothing is set", func(t *testing.T) {
		t.Setenv(ProfileEnv, "")
		p, err := cfg.Resolve("")
		if err != nil || p.DataSourceID != "ds-work" {
			t.Fatalf("got %+v, err %v", p, err)
		}
	})

	t.Run("env beats the default profile", func(t *testing.T) {
		t.Setenv(ProfileEnv, "env")
		p, err := cfg.Resolve("")
		if err != nil || p.DataSourceID != "ds-env" {
			t.Fatalf("got %+v, err %v", p, err)
		}
	})

	t.Run("an explicit name beats the env", func(t *testing.T) {
		t.Setenv(ProfileEnv, "env")
		p, err := cfg.Resolve("flag")
		if err != nil || p.DataSourceID != "ds-flag" {
			t.Fatalf("got %+v, err %v", p, err)
		}
	})

	t.Run("a per-value env var overrides the resolved profile", func(t *testing.T) {
		t.Setenv(ProfileEnv, "")
		t.Setenv(DataSourceEnv, "ds-override")
		p, err := cfg.Resolve("")
		if err != nil || p.DataSourceID != "ds-override" {
			t.Fatalf("got %+v, err %v", p, err)
		}
	})
}
