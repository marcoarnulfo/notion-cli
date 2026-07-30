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
	// MeEnv is the environment override for "--assignee me": it wins over
	// both credentials.yml and the profile's legacy me: field. See
	// ResolveIdentity for the full precedence.
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
	// Me is the legacy location for the assignee value "--assignee me"
	// resolves to: the last resort in ResolveIdentity's precedence, kept
	// readable forever so a config written before the identity moved to
	// credentials.yml keeps working. Optional.
	Me string `yaml:"me,omitempty"`
	// MeSource records where the effective identity came from: "env",
	// "file", "legacy", or "" when there is none. Populated by
	// ResolveIdentity and never persisted — it describes this run, not the
	// configuration. doctor reads it to tell a user whose identity is still
	// in the shared config file how to move it.
	MeSource string `yaml:"-"`
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

// Resolve returns a profile by name, falling back to NOTION_TRACK_PROFILE and
// then to default_profile. Environment overrides are applied last so that CI
// can point an existing profile at another data source.
func (c *Config) Resolve(name string) (Profile, error) {
	name = c.ProfileName(name)

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
	// Identities maps a profile name to the value "--assignee me" resolves
	// to for this user. It lives here rather than in config.yml for the same
	// reason the token does: it is personal, and config.yml is meant to be
	// committed. Keyed by profile because two profiles can point at
	// workspaces that spell the same person differently.
	Identities map[string]string `yaml:"identities,omitempty"`
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

	creds, err := loadCredentials()
	if err != nil {
		return "", "", err
	}
	if creds.Token == "" {
		return "", "", nil
	}
	return creds.Token, "file", nil
}

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
		// err is already a *fs.PathError that names path itself (e.g. "open
		// /…/credentials.yml: permission denied"); wrapping it inside
		// another "reading %s:" prefix would repeat that path a second
		// time. %v here keeps the underlying reason once; %w on
		// ErrCredentialsUnreadable is what lets callers map this to
		// ExitAuth via errors.Is.
		return Credentials{}, fmt.Errorf("config: %w: %v", ErrCredentialsUnreadable, err)
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
		return Credentials{}, fmt.Errorf("config: %s: %w", path, ErrInvalidCredentials)
	}
	return creds, nil
}

// writeCredentials writes creds to credentials.yml, atomically (a temp file
// in the same directory, then rename) and with restrictive permissions.
// Both SaveToken and SaveIdentity funnel through here so there is exactly
// one place on disk this file is ever written.
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
func writeCredentials(creds Credentials) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("config: creating config dir: %w", err)
	}

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

// SaveToken writes the token to credentials.yml. Called only from init, and
// only after an interactive user has explicitly opted in. It reads the file
// first so that saving a token cannot discard the identities sitting next to
// it — see SaveIdentity, which faces the same hazard in the other direction.
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

// MeSourceUnreadable is the MeSource of a run whose credentials file could not
// be read at all. ResolveIdentity never returns it — it reports that as an
// error — so it is the caller that turns the failure into this source, which
// is what lets the two places that actually need an identity tell "nobody
// configured one" from "one may well be on file, but nothing could read it".
// Naming it here rather than spelling the string twice keeps the writer
// (internal/cli.buildService) and the readers (service.resolveAssignee,
// doctor's assignee check) from drifting apart.
const MeSourceUnreadable = "unreadable"

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
