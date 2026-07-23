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
