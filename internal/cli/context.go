package cli

import (
	"errors"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/spf13/cobra"
)

// Package-level seams. Tests replace them instead of touching the filesystem.
var (
	loadConfig     = config.Load
	loadConfigFrom = config.LoadFrom
	newClient      = func(token string) *notion.Client { return notion.New(token) }
)

// buildService resolves token, config and profile into a ready Service.
func buildService(cmd *cobra.Command) (*service.Service, error) {
	token, _, err := config.LoadToken()
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, Errorf(ExitAuth,
			"no integration token found\n"+
				"  set %s, or run 'notion-track init'\n"+
				"  a workspace owner creates the token at https://www.notion.so/my-integrations",
			config.TokenEnv)
	}

	path, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfigForFlag(path)
	if err != nil {
		return nil, err
	}

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
		// Not fatal, and deliberately not a fallback either. This runs for
		// every command, but almost none of them need an identity: failing
		// here would let an unreadable credentials.yml take down `list` and
		// `get`, which never ask who "me" is. Falling through to the profile's
		// legacy me: would be worse still — that value is whoever ran init,
		// and silently assigning work to the wrong person is the exact failure
		// the identity moved out of the shared file to prevent. So the
		// identity stays empty and the source records why, leaving the two
		// places that do need one — `--assignee me` and doctor's assignee
		// check — free to report it instead of guessing.
		me, source = "", config.MeSourceUnreadable
	}
	profile.Me, profile.MeSource = me, source

	if profile.DataSourceID == "" {
		return nil, Errorf(ExitUsage,
			"profile has no data_source_id; run 'notion-track init' to configure it")
	}
	return service.New(newClient(token), profile), nil
}

// loadConfigForFlag reads the config --config points at, or the default
// location when the flag was not given. Unlike loadExistingOrNew below it lets
// ErrNotConfigured through: its callers are the ones that need an existing
// profile to read, not to write one.
func loadConfigForFlag(path string) (*config.Config, error) {
	if path != "" {
		return loadConfigFrom(path)
	}
	return loadConfig()
}

// loadExistingOrNew returns the config at path, or an empty one if absent.
func loadExistingOrNew(path string) (*config.Config, error) {
	cfg, err := loadConfigForFlag(path)
	if errors.Is(err, config.ErrNotConfigured) {
		return &config.Config{
			SchemaVersion: config.CurrentSchemaVersion,
			Profiles:      map[string]config.Profile{},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]config.Profile{}
	}
	return cfg, nil
}

// saveConfigTo writes to an explicit path when given, otherwise the default.
func saveConfigTo(cfg *config.Config, path string) error {
	if path == "" {
		return cfg.Save()
	}
	return cfg.SaveTo(path)
}
