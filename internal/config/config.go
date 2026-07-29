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
	// MeEnv names the person "--assignee me" stands for. It is an environment
	// variable first and a profile field second on purpose: config.yml is meant
	// to be committed and shared (see Credentials), so an identity stored there
	// would be everyone's identity — and would silently assign tasks to whoever
	// committed the file.
	MeEnv = "NOTION_TRACK_ME"
)

// Properties maps notion-track's concepts onto real property names.
// Nothing here is hardcoded: init discovers these from the data source.
type Properties struct {
	Ticket string `yaml:"ticket"`
	Status string `yaml:"status"`
	Title  string `yaml:"title"`
	Due    string `yaml:"due,omitempty"`
	// Assignee is the column naming who a row belongs to. Optional: a board
	// that tracks nobody in particular simply leaves it unmapped.
	Assignee string `yaml:"assignee,omitempty"`
	// Priority is the column ranking how urgent a row is. Optional, and
	// usually sparse: a board marks what is burning, not everything.
	Priority string `yaml:"priority,omitempty"`
	// ID is the column carrying Notion's own row identifier ("BDF-271").
	// Optional, and read-only by nature: it is a way to address a row, never a
	// value to write, which is why nothing in tracker.BuildProperties knows it
	// exists.
	ID string `yaml:"id,omitempty"`
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
	// Me is the assignee value "--assignee me" resolves to, overridden by
	// MeEnv. Optional.
	Me string `yaml:"me,omitempty"`
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
	if v := os.Getenv(MeEnv); v != "" {
		p.Me = v
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

// ErrCredentialsUnreadable signals a credentials.yml that exists but could
// not be read (permissions, a directory in its place, and so on) — distinct
// from simply being absent, which LoadToken treats as "no token here" and
// is not an error at all. Callers map this to the same exit code every
// other authentication failure uses: without a readable credentials file
// there may be a token nobody can prove exists, and that is an auth
// problem, not a generic one.
var ErrCredentialsUnreadable = errors.New("credentials file unreadable")

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
		// err is already a *fs.PathError that names path itself (e.g. "open
		// /…/credentials.yml: permission denied"); wrapping it inside
		// another "reading %s:" prefix would repeat that path a second
		// time. %v here keeps the underlying reason once; %w on
		// ErrCredentialsUnreadable is what lets callers map this to
		// ExitAuth via errors.Is.
		return "", "", fmt.Errorf("config: %w: %v", ErrCredentialsUnreadable, err)
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

	// A build from before CreateTemp's random suffix replaced this fixed
	// name may have left one behind — os.WriteFile does not apply its mode
	// to a file that already exists, so a leftover here can be
	// world-readable, holding a token in the clear right next to
	// credentials.yml. Nothing reads this fixed name any more (LoadToken
	// never has), so nothing would ever notice or remove it otherwise;
	// best-effort, since a missing or unremovable leftover must never block
	// persisting the token this call exists to save.
	_ = os.Remove(path + ".tmp")

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
