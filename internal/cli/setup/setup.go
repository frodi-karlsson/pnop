// Package setup records credential configs and switches between them.
package setup

import (
	"context"
	"errors"

	"github.com/frodi-karlsson/pnop/internal/config"
	"github.com/frodi-karlsson/pnop/internal/logger"
	"github.com/frodi-karlsson/pnop/internal/npmrc"
	"github.com/frodi-karlsson/pnop/internal/secret"
	"github.com/spf13/cobra"
)

// Deps are the collaborators Run needs, injected for testability.
type Deps struct {
	// ConfigPath is where the config document lives.
	ConfigPath string
	Secret     secret.Fetcher
	Npmrc      npmrc.Store
	LoadConfig func(path string) (config.Config, error)
	SaveConfig func(path string, cfg config.Config) error
	// Log receives progress messages, never the token itself.
	Log logger.Logger
}

// Command returns the `pnop setup` subcommand.
func Command(load func() (Deps, error)) *cobra.Command {
	var name string
	entry := config.Entry{}

	cmd := &cobra.Command{
		Use:   "setup -c <name>",
		Short: "Switch to a credential config, creating it if flags are given",
		Long: "Activate a named credential config and write its token to the npmrc it\n" +
			"manages. With --vault/--item/--field the config is created or updated\n" +
			"first. Later pnop commands need no flags.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, err := load()
			if err != nil {
				return err
			}
			return Run(cmd.Context(), deps, name, entry)
		},
	}

	cmd.Flags().StringVarP(&name, "config", "c", "", "name of the config to activate (required)")
	cmd.Flags().StringVar(&entry.File, "file", "", "npmrc file this config keeps in sync")
	cmd.Flags().StringVar(&entry.Vault, "vault", "", "1Password vault holding the token")
	cmd.Flags().StringVar(&entry.Item, "item", "", "1Password item holding the token")
	cmd.Flags().StringVar(&entry.Field, "field", "", "field on the item holding the token")
	cmd.Flags().StringVar(&entry.Registry, "registry", "", "registry whose _authToken is managed")

	return cmd
}

// Run activates the named config, creating or updating it first when flags
// supply one. Activation fetches the token and writes it, so a single command
// is a complete profile switch.
//
// Ordering matters: the token is fetched and written before the config is
// saved, so a vault reference that cannot be read is never recorded as active.
func Run(ctx context.Context, d Deps, name string, flags config.Entry) error {
	if name == "" {
		return errors.New("a config name is required: pnop setup -c <name>")
	}

	cfg, err := d.LoadConfig(d.ConfigPath)
	if err != nil && !errors.Is(err, config.ErrNotConfigured) {
		return err
	}
	if cfg.Configs == nil {
		cfg.Configs = map[string]config.Entry{}
	}

	entry, err := resolveEntry(cfg, name, flags)
	if err != nil {
		return err
	}

	token, err := d.Secret.Fetch(ctx, entry.Vault, entry.Item, entry.Field)
	if err != nil {
		return err
	}
	if err := d.Npmrc.WriteToken(entry.File, entry.Registry, token); err != nil {
		return err
	}

	cfg.Configs[name] = entry
	cfg.Active = name
	if err := d.SaveConfig(d.ConfigPath, cfg); err != nil {
		return err
	}

	d.Log.Infof("active config is now %q", name)
	d.Log.Infof("wrote %s", entry.File)
	return nil
}

// resolveEntry merges any supplied flags over the stored entry, or builds a
// new one. An unknown name with no flags is an error rather than an empty
// config.
func resolveEntry(cfg config.Config, name string, flags config.Entry) (config.Entry, error) {
	entry, known := cfg.Configs[name]
	if !known && flags == (config.Entry{}) {
		return config.Entry{}, &config.UnknownConfigError{Name: name, Known: cfg.Names()}
	}

	if flags.File != "" {
		entry.File = flags.File
	}
	if flags.Vault != "" {
		entry.Vault = flags.Vault
	}
	if flags.Item != "" {
		entry.Item = flags.Item
	}
	if flags.Field != "" {
		entry.Field = flags.Field
	}
	if flags.Registry != "" {
		entry.Registry = flags.Registry
	}

	entry = entry.WithDefaults()
	if err := entry.Validate(); err != nil {
		return config.Entry{}, err
	}

	// Store the resolved path: a "~" recorded in config would have to be
	// re-expanded on every run, and could resolve differently under sudo.
	file, err := config.ExpandPath(entry.File)
	if err != nil {
		return config.Entry{}, err
	}
	entry.File = file
	return entry, nil
}
