// Package setup persists pnop's configuration and writes the token once.
package setup

import (
	"context"

	"github.com/frodi-karlsson/pnop/internal/config"
	"github.com/frodi-karlsson/pnop/internal/logger"
	"github.com/frodi-karlsson/pnop/internal/npmrc"
	"github.com/frodi-karlsson/pnop/internal/secret"
	"github.com/spf13/cobra"
)

// Deps are the collaborators Run needs, injected for testability.
type Deps struct {
	// ConfigPath is where the config file is written.
	ConfigPath string
	Secret     secret.Fetcher
	Npmrc      npmrc.Store
	// SaveConfig persists the validated config.
	SaveConfig func(path string, cfg config.Config) error
	// Log receives progress messages, never the token itself.
	Log logger.Logger
}

// Command returns the `pnop setup` subcommand.
func Command(load func() (Deps, error)) *cobra.Command {
	cfg := config.Config{}

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Record where the npm token lives and write it to the npmrc",
		Long: "Record which npmrc pnop keeps in sync and where its token lives in 1Password,\n" +
			"then fetch the token once. Later runs of `pnop` need no flags.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, err := load()
			if err != nil {
				return err
			}
			return Run(cmd.Context(), deps, cfg)
		},
	}

	cmd.Flags().StringVar(&cfg.File, "file", "~/.npmrc", "npmrc file pnop keeps in sync")
	cmd.Flags().StringVar(&cfg.Vault, "vault", "", "1Password vault holding the token (required)")
	cmd.Flags().StringVar(&cfg.Item, "item", "", "1Password item holding the token (required)")
	cmd.Flags().StringVar(&cfg.Field, "field", "", "field on the item holding the token (required)")
	cmd.Flags().StringVar(&cfg.Registry, "registry", npmrc.DefaultRegistry, "registry whose _authToken is managed")

	return cmd
}

// Run validates the config, saves it, then fetches and writes the token so
// setup leaves a working npmrc behind rather than only a config file.
func Run(ctx context.Context, d Deps, cfg config.Config) error {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}

	// Store the resolved path: a ~ recorded in config would have to be
	// re-expanded on every run, and could resolve differently under sudo.
	file, err := config.ExpandPath(cfg.File)
	if err != nil {
		return err
	}
	cfg.File = file

	token, err := d.Secret.Fetch(ctx, cfg.Vault, cfg.Item, cfg.Field)
	if err != nil {
		return err
	}
	if err := d.Npmrc.WriteToken(cfg.File, cfg.Registry, token); err != nil {
		return err
	}
	if err := d.SaveConfig(d.ConfigPath, cfg); err != nil {
		return err
	}

	d.Log.Infof("wrote %s", cfg.File)
	d.Log.Infof("wrote %s", d.ConfigPath)
	return nil
}
