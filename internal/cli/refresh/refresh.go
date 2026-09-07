// Package refresh rewrites the active config's npmrc from 1Password.
package refresh

import (
	"context"

	"github.com/frodi-karlsson/pnop/internal/cli/setup"
	"github.com/spf13/cobra"
)

// Command returns the `pnop +refresh` subcommand.
func Command(load func() (setup.Deps, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "+refresh",
		Short: "Rewrite the active config's npmrc with a fresh token",
		Long: "Refetch the active config's token from 1Password and write it to the\n" +
			"npmrc it manages, without changing which config is active.\n\n" +
			"The `+` keeps this out of pnpm's way: a repo script named `refresh` is\n" +
			"still just `pnpm refresh`.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, err := load()
			if err != nil {
				return err
			}
			return Run(cmd.Context(), deps)
		},
	}
}

// Run refetches the active config's token and writes it.
func Run(ctx context.Context, d setup.Deps) error {
	cfg, err := d.LoadConfig(d.ConfigPath)
	if err != nil {
		return err
	}
	entry, err := cfg.ActiveEntry()
	if err != nil {
		return err
	}

	setup.WarnLegacy(d, cfg.Active, entry)

	if err := setup.Apply(ctx, d, entry); err != nil {
		return err
	}

	d.Log.Infof("refreshed %s from config %q", entry.File, cfg.Active)
	return nil
}
