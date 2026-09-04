// Package passthrough runs any pnpm command and recovers from a stale npm token.
package passthrough

import (
	"context"

	"github.com/frodi-karlsson/pnop/internal/cli"
	"github.com/frodi-karlsson/pnop/internal/config"
	"github.com/frodi-karlsson/pnop/internal/logger"
	"github.com/frodi-karlsson/pnop/internal/npmrc"
	"github.com/frodi-karlsson/pnop/internal/runner"
	"github.com/frodi-karlsson/pnop/internal/secret"
)

// PackageManager is the command pnop wraps.
const PackageManager = "pnpm"

// Deps are the collaborators Run needs. They are injected so the decision
// logic can be tested without a real pnpm, 1Password or filesystem.
type Deps struct {
	// LoadEntry resolves the active credential config. It is called only when
	// a command has already failed, so pnop works as a plain pnpm alias
	// before `pnop setup` has ever been run.
	LoadEntry func() (config.Entry, error)
	Secret    secret.Fetcher
	Npmrc     npmrc.Store
	Runner    runner.Runner
	// Log receives pnop's own progress messages, never the token itself.
	Log logger.Logger
}

// Run executes pnpm with args forwarded verbatim. On success it returns nil.
// On failure it compares the token in the managed npmrc against 1Password: if
// they already match, the failure was not a stale token and the original exit
// code is returned untouched. Otherwise the npmrc is refreshed and the command
// rerun once.
//
// pnop deliberately does not inspect the command's output. The token
// comparison is the only gate, and it doubles as a safety gate: a rerun only
// happens when the token actually changed, which means the first attempt was
// rejected by the registry and cannot have had a side effect.
func Run(ctx context.Context, d Deps, args []string) error {
	code, err := d.Runner.Run(ctx, PackageManager, args...)
	if err != nil {
		return err
	}
	if code == 0 {
		return nil
	}

	if runner.Signalled(code) {
		// The child was killed rather than rejected; credentials are not the
		// problem, so don't prompt 1Password or rerun.
		return cli.Exit(code)
	}

	d.Log.Infof("%s failed (exit %d) - checking the npm token...", PackageManager, code)

	entry, err := d.LoadEntry()
	if err != nil {
		d.Log.Warnf("%v", err)
		return cli.Exit(code)
	}

	refreshed, err := refreshToken(ctx, d, entry)
	if err != nil {
		d.Log.Warnf("%v", err)
		return cli.Exit(code)
	}
	if !refreshed {
		d.Log.Infof("token is already current - the failure above is something else.")
		return cli.Exit(code)
	}

	d.Log.Infof("token was stale, updated %s - retrying...", entry.File)

	retryCode, err := d.Runner.Run(ctx, PackageManager, args...)
	if err != nil {
		return err
	}
	return cli.Exit(retryCode)
}

// refreshToken reports whether the managed npmrc was actually changed.
func refreshToken(ctx context.Context, d Deps, entry config.Entry) (bool, error) {
	current, err := d.Npmrc.ReadToken(entry.File, entry.Registry)
	if err != nil {
		return false, err
	}

	fresh, err := d.Secret.Fetch(ctx, entry.Vault, entry.Item, entry.Field)
	if err != nil {
		return false, err
	}

	if fresh == current {
		return false, nil
	}
	if err := d.Npmrc.WriteToken(entry.File, entry.Registry, fresh); err != nil {
		return false, err
	}
	return true, nil
}
