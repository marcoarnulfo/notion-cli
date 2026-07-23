// Package config reads and writes notion-track's YAML configuration.
//
// Two rules shape this file. First, the token never lands on disk by accident:
// a token read from the environment is remembered as such, and Save skips it.
// Second, Load may warn on stderr, Save must stay silent.
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

// Token returns the integration token and whether it came from the
// environment. Callers must never persist a token whose second return value
// is true, otherwise a CI secret ends up on a developer's disk.
func Token() (string, bool) {
	if v := os.Getenv(TokenEnv); v != "" {
		return v, true
	}
	return "", false
}
