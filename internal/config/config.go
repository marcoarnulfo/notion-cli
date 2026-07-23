// Package config reads and writes notion-track's YAML configuration.
//
// Three rules shape this file. First, the token never lands on disk by
// accident: a token read from the environment is remembered as such, and
// Save (config.yml) skips it entirely. Second, the token that does get
// persisted lives in its own file, credentials.yml, never in config.yml —
// see Credentials for why that split matters. Third, Load may warn on
// stderr, Save must stay silent.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// CurrentSchemaVersion is bumped whenever the on-disk shape changes.
const CurrentSchemaVersion = 1

// Environment variables, all overriding the config file.
const (
	TokenEnv      = "NOTION_TOKEN"
	ProfileEnv    = "NOTION_TRACK_PROFILE"
	DatabaseEnv   = "NOTION_TRACK_DB"
	DataSourceEnv = "NOTION_TRACK_DATA_SOURCE"
)

// Properties maps notion-track's concepts onto real property names.
// Nothing here is hardcoded: init discovers these from the data source.
type Properties struct {
	Ticket string `yaml:"ticket"`
	Status string `yaml:"status"`
	Title  string `yaml:"title"`
	Due    string `yaml:"due,omitempty"`
}

// Profile is one configured data source.
type Profile struct {
	DatabaseID   string     `yaml:"database_id"`
	DataSourceID string     `yaml:"data_source_id"`
	Properties   Properties `yaml:"properties"`
	// StatusType is "status" or "select". It decides the payload shape and,
	// more importantly, how strict validation has to be: a select silently
	// creates unknown options, a status rejects them.
	StatusType string `yaml:"status_type"`
}

// Config is the whole file.
type Config struct {
	SchemaVersion  int                `yaml:"schema_version"`
	DefaultProfile string             `yaml:"default_profile"`
	Profiles       map[string]Profile `yaml:"profiles"`
}

// configPath is a seam: tests point it at t.TempDir().
var configPath = defaultConfigPath

func defaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locating config dir: %w", err)
	}
	return filepath.Join(dir, "notion-track", "config.yml"), nil
}

// ErrNotConfigured signals that no config file exists yet.
var ErrNotConfigured = errors.New("config: not configured; run 'notion-track init'")

// Load reads the config from its default location.
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads the config from an explicit path.
func LoadFrom(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	migrate(&cfg)
	return &cfg, nil
}

// SaveTo writes the config to an explicit path.
func (c *Config) SaveTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: creating config dir: %w", err)
	}
	c.SchemaVersion = CurrentSchemaVersion
	raw, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("config: encoding: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("config: writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("config: replacing %s: %w", path, err)
	}
	return nil
}

// Save writes the config to its default location.
func (c *Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	return c.SaveTo(path)
}

// Resolve returns a profile by name, falling back to NOTION_TRACK_PROFILE and
// then to default_profile. Environment overrides are applied last so that CI
// can point an existing profile at another data source.
func (c *Config) Resolve(name string) (Profile, error) {
	if name == "" {
		name = os.Getenv(ProfileEnv)
	}
	if name == "" {
		name = c.DefaultProfile
	}

	p, ok := c.Profiles[name]
	if !ok {
		names := make([]string, 0, len(c.Profiles))
		for n := range c.Profiles {
			names = append(names, n)
		}
		sort.Strings(names)
		return Profile{}, fmt.Errorf(
			"config: no profile %q; available profiles: %s", name, strings.Join(names, ", "))
	}

	if v := os.Getenv(DatabaseEnv); v != "" {
		p.DatabaseID = v
	}
	if v := os.Getenv(DataSourceEnv); v != "" {
		p.DataSourceID = v
	}
	return p, nil
}

// Token returns the integration token from the environment only, and
// whether it was found. It is the seam LoadToken uses to make NOTION_TOKEN
// win over the file: CI passes its token this way and must never have that
// secret read back off, or written to, disk.
func Token() (string, bool) {
	if v := os.Getenv(TokenEnv); v != "" {
		return v, true
	}
	return "", false
}

// Credentials is the on-disk shape of credentials.yml.
//
// It exists as a file separate from config.yml on purpose: config.yml holds
// no secret and is meant to be committed to a project repo so CI and every
// teammate share the same property mapping. If the token lived in the same
// file, every commit of that file would risk leaking it. Splitting the
// files makes "never commit the token" a property of the filesystem layout
// instead of a rule a human has to remember to follow.
type Credentials struct {
	SchemaVersion int    `yaml:"schema_version"`
	Token         string `yaml:"token"`
}

// credentialsPath is a seam: tests point it at t.TempDir().
var credentialsPath = defaultCredentialsPath

func defaultCredentialsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locating config dir: %w", err)
	}
	return filepath.Join(dir, "notion-track", "credentials.yml"), nil
}

// CredentialsPath returns the location credentials.yml is read from and
// written to, for messages that need to name it (doctor's token source,
// init's save confirmation).
func CredentialsPath() (string, error) { return credentialsPath() }

// ErrInvalidCredentials signals a credentials.yml that failed to parse. It
// deliberately carries no detail from the file itself: see LoadToken.
var ErrInvalidCredentials = errors.New("credentials file is not valid YAML; delete it and rerun 'notion-track init'")

// LoadToken resolves the integration token the way every command does:
// NOTION_TOKEN first, then credentials.yml. source is "env" or "file"
// (empty when no token was found anywhere), which is what lets doctor tell
// a user who has different tokens in both places which one actually won.
func LoadToken() (token string, source string, err error) {
	if v, ok := Token(); ok {
		return v, "env", nil
	}

	path, err := credentialsPath()
	if err != nil {
		return "", "", err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("config: reading %s: %w", path, err)
	}

	var creds Credentials
	if err := yaml.Unmarshal(raw, &creds); err != nil {
		// yaml.v3's unmarshal error names the offending scalar (the value
		// itself, up to 10 characters, more if truncated). Every scalar in
		// this file is either a token or a mistaken paste of one, so that
		// error is a partial secret leak by construction — the underlying
		// yaml error must never be wrapped with %w. ErrInvalidCredentials is
		// a fixed message a caller can still key on with errors.Is; it
		// carries nothing of what was actually read.
		return "", "", fmt.Errorf("config: %s: %w", path, ErrInvalidCredentials)
	}
	if creds.Token == "" {
		return "", "", nil
	}
	return creds.Token, "file", nil
}

// SaveToken writes the token to credentials.yml, atomically (a temp file in
// the same directory, then rename) and with restrictive permissions.
// Called only from init, and only after an interactive user has explicitly
// opted in.
//
// The temp file is created with os.CreateTemp rather than a fixed name plus
// os.WriteFile: WriteFile does not apply its mode to a file that already
// exists (it opens without O_EXCL and truncates, keeping whatever
// permissions were already there), and a fixed, predictable name invites
// exactly that — a crash between write and rename, a sync tool, a restore
// from backup, or another process of the same user can all leave a residual
// file at a guessable path. CreateTemp picks an unpredictable suffix and
// opens with O_EXCL, so nothing pre-existing can be reused; the explicit
// Chmod after that closes the umask gap CreateTemp's own 0600 default still
// leaves (belt and suspenders, not strictly required, but the cost of
// skipping it is a leaked secret).
func SaveToken(token string) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("config: creating config dir: %w", err)
	}

	creds := Credentials{SchemaVersion: CurrentSchemaVersion, Token: token}
	raw, err := yaml.Marshal(&creds)
	if err != nil {
		return fmt.Errorf("config: encoding credentials: %w", err)
	}

	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("config: creating temp file: %w", err)
	}
	tmp := f.Name()
	// Any exit from here on must not leave the temp file behind with the
	// token still in it.
	defer os.Remove(tmp)

	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("config: setting permissions on %s: %w", tmp, err)
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return fmt.Errorf("config: writing %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("config: writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("config: replacing %s: %w", path, err)
	}
	return nil
}
