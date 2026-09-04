// Package install runs `pnpm install` and recovers from a stale npm token.
package install

import (
	"context"

	"github.com/frodi-karlsson/pnop/internal/cli"
	"github.com/frodi-karlsson/pnop/internal/config"
	"github.com/frodi-karlsson/pnop/internal/logger"
	"github.com/frodi-karlsson/pnop/internal/npmrc"
	"github.com/frodi-karlsson/pnop/internal/runner"
	"github.com/frodi-karlsson/pnop/internal/secret"
	"github.com/spf13/cobra"
)

// PackageManager is the command pnop wraps.
const PackageManager = "pnpm"

// Deps are the collaborators Run needs. They are injected so the decision
// logic can be tested without a real pnpm, 1Password or filesystem.
type Deps struct {
	Config config.Config
	Secret secret.Fetcher
	Npmrc  npmrc.Store
	Runner runner.Runner
	// Log receives pnop's own progress messages, never the token itself.
	Log logger.Logger
}

// Command returns the explicit `pnop install` subcommand. Bare `pnop` reaches
// the same code path via Run.
func Command(load func() (Deps, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "install [pnpm args...]",
		Short: "Run pnpm install, refreshing a stale npm token if it fails",
		// pnpm's own flags must reach pnpm rather than being parsed here.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := load()
			if err != nil {
				return err
			}
			return Run(cmd.Context(), deps, args)
		},
		SilenceUsage: true,
	}
}

// Run executes `pnpm install`. On success it returns nil. On failure it
// compares the token in the managed npmrc against 1Password: if they already
// match, the failure was not a stale token and the original exit code is
// returned untouched. Otherwise the npmrc is refreshed and the install retried.
func Run(ctx context.Context, d Deps, args []string) error {
	code, err := d.Runner.Run(ctx, PackageManager, append([]string{"install"}, args...)...)
	if err != nil {
		return err
	}
	if code == 0 {
		return nil
	}

	if runner.Signalled(code) {
		// The child was killed rather than rejected; credentials are not the
		// problem, so don't prompt 1Password or retry.
		return cli.Exit(code)
	}

	d.Log.Infof("%s install failed (exit %d) - checking the npm token...", PackageManager, code)

	refreshed, err := refreshToken(ctx, d)
	if err != nil {
		d.Log.Warnf("%v", err)
		return cli.Exit(code)
	}
	if !refreshed {
		d.Log.Infof("token is already current - the failure above is something else.")
		return cli.Exit(code)
	}

	d.Log.Infof("token was stale, updated %s - retrying...", d.Config.File)

	retryCode, err := d.Runner.Run(ctx, PackageManager, append([]string{"install"}, args...)...)
	if err != nil {
		return err
	}
	return cli.Exit(retryCode)
}

// refreshToken reports whether the managed npmrc was actually changed.
func refreshToken(ctx context.Context, d Deps) (bool, error) {
	current, err := d.Npmrc.ReadToken(d.Config.File, d.Config.Registry)
	if err != nil {
		return false, err
	}

	fresh, err := d.Secret.Fetch(ctx, d.Config.Vault, d.Config.Item, d.Config.Field)
	if err != nil {
		return false, err
	}

	if fresh == current {
		return false, nil
	}
	if err := d.Npmrc.WriteToken(d.Config.File, d.Config.Registry, fresh); err != nil {
		return false, err
	}
	return true, nil
}
