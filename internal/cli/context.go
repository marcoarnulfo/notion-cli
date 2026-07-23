package cli

import (
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
