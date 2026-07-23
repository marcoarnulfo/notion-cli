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
	token, _ := config.Token()
	if token == "" {
		return nil, Errorf(ExitAuth,
			"no integration token found\n"+
				"  set %s, or run 'notion-track init'\n"+
				"  a workspace owner creates the token at https://www.notion.so/my-integrations",
			config.TokenEnv)
	}

	path, _ := cmd.Flags().GetString("config")
	var (
		cfg *config.Config
		err error
	)
	if path != "" {
		cfg, err = loadConfigFrom(path)
	} else {
		cfg, err = loadConfig()
	}
	if err != nil {
		return nil, err
	}

	profileName, _ := cmd.Flags().GetString("profile")
	profile, err := cfg.Resolve(profileName)
	if err != nil {
		return nil, Errorf(ExitUsage, "%v", err)
	}
	if profile.DataSourceID == "" {
		return nil, Errorf(ExitUsage,
			"profile has no data_source_id; run 'notion-track init' to configure it")
	}
	return service.New(newClient(token), profile), nil
}

// loadExistingOrNew returns the config at path, or an empty one if absent.
func loadExistingOrNew(path string) (*config.Config, error) {
	var (
		cfg *config.Config
		err error
	)
	if path != "" {
		cfg, err = loadConfigFrom(path)
	} else {
		cfg, err = loadConfig()
	}
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
