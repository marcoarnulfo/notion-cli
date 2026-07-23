package config

import (
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
