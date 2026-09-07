// Package passthrough runs any pnpm command and recovers from a stale npm token.
package passthrough

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/frodi-karlsson/pnop/internal/cli"
	"github.com/frodi-karlsson/pnop/internal/config"
	"github.com/frodi-karlsson/pnop/internal/logger"
	"github.com/frodi-karlsson/pnop/internal/negcache"
	"github.com/frodi-karlsson/pnop/internal/npmrc"
	"github.com/frodi-karlsson/pnop/internal/runner"
	"github.com/frodi-karlsson/pnop/internal/secret"
	"github.com/frodi-karlsson/pnop/internal/verify"
)

// PackageManager is the command pnop wraps.
const PackageManager = "pnpm"

// Environment pnop reads. BinEnv names the real pnpm for a PATH shim that
// would otherwise resolve back to itself; MarkerEnv is a path a wrapper owns,
// never one pnop picks, since a fixed path is shared between shells.
const (
	BinEnv     = "PNOP_PNPM"
	RetriedEnv = "PNOP_RETRIED"
	RerunEnv   = "PNOP_RERUN"
	MarkerEnv  = "PNOP_REFRESH_MARKER"
)

// Deps are the collaborators Run needs, injected for testing. LoadEntry is
// called only after a command has failed, so pnop works as a plain pnpm alias
// before `pnop setup` has ever been run.
type Deps struct {
	LoadEntry   func() (string, config.Entry, error)
	Secret      secret.Fetcher
	Npmrc       npmrc.Store
	Runner      runner.Runner
	Verifier    verify.Verifier
	Cache       negcache.Cache
	Getenv      func(string) string
	WriteMarker func(path string) error
	Log         logger.Logger
}

// Run executes pnpm with args forwarded verbatim, and on failure asks the
// registry whether the token on disk is still accepted. Only a 401 justifies
// reading 1Password: a 200 means the failure was something else, and anything
// else is about the endpoint rather than the credential. A refresh writes the
// npmrc and prints the command rather than rerunning it, since pnop cannot see
// whether the first attempt had a side effect.
func Run(ctx context.Context, d Deps, args []string) error {
	bin := packageManager(d)

	res, err := d.Runner.Run(ctx, bin, args...)
	if err != nil {
		return err
	}
	if res.Code == 0 {
		return nil
	}
	code := res.Code

	if runner.Signalled(code) || getenv(d, RetriedEnv) != "" {
		return cli.Exit(code)
	}

	name, entry, err := d.LoadEntry()
	if err != nil {
		d.Log.Warnf("%v", err)
		return cli.Exit(code)
	}

	disk, err := d.Npmrc.ReadToken(entry.File, entry.Registry)
	if err != nil {
		d.Log.Warnf("%v", err)
		return cli.Exit(code)
	}
	disk = strings.TrimSpace(disk)

	// With no token there is nothing to ask about: an unauthenticated whoami
	// answers 401, which would report a credential that does not exist as
	// rejected. Go to the vault instead.
	if disk != "" && d.Verifier.Verify(ctx, entry.Registry, disk) != verify.Rejected {
		return cli.Exit(code)
	}

	if cached, ok := lookup(d, name, disk); ok {
		// Naming pnpm's failure keeps this from reading as a diagnosis of it.
		d.Log.Infof("%s failed above; pnop did not refresh - %s", PackageManager, cached.Describe())
		return cli.Exit(code)
	}

	stored, err := d.Secret.Fetch(ctx, entry.Vault, entry.Item, entry.Field)
	if err != nil {
		d.Log.Warnf("%v", err)
		return cli.Exit(code)
	}
	fresh := npmrc.NormalizeToken(stored) // the item may hold a whole npmrc line

	switch d.Verifier.Verify(ctx, entry.Registry, fresh) {
	case verify.Valid:
		return refresh(ctx, d, entry, fresh, args, code, bin, true)

	case verify.Rejected:
		record(d, name, disk, negcache.Rejected)
		if fresh == disk {
			d.Log.Warnf("the vault and %s hold the same rejected token", entry.File)
		} else {
			d.Log.Warnf("the vault has a newer token and it is also rejected")
		}
		return cli.Exit(code)

	default:
		// Unverifiable, but the disk token's 401 was definite, so a different
		// token cannot be worse.
		if fresh == disk {
			record(d, name, disk, negcache.Unchecked)
			d.Log.Infof("the vault holds the same token as %s and the registry could not confirm it", entry.File)
			return cli.Exit(code)
		}
		return refresh(ctx, d, entry, fresh, args, code, bin, false)
	}
}

// refresh writes the token and says what to do next.
func refresh(ctx context.Context, d Deps, entry config.Entry, token string, args []string, code int, bin string, verified bool) error {
	if err := d.Npmrc.WriteToken(entry.File, entry.Registry, token); err != nil {
		d.Log.Warnf("%v", err)
		return cli.Exit(code)
	}
	if verified {
		d.Log.Infof("refreshed the npm token in %s", entry.File)
	} else {
		d.Log.Infof("wrote the vault's token to %s, though the registry could not confirm it", entry.File)
	}
	markRefreshed(d)

	if !rerunWanted(d, entry) {
		d.Log.Infof("run it again: %s %s", PackageManager, quote(args))
		return cli.Exit(code)
	}

	d.Log.Infof("rerunning: %s %s", PackageManager, quote(args))
	retry, err := d.Runner.RunEnv(ctx, []string{RetriedEnv + "=1"}, bin, args...)
	if err != nil {
		return err
	}
	return cli.Exit(retry.Code)
}

func rerunWanted(d Deps, entry config.Entry) bool {
	if entry.Rerun {
		return true
	}
	switch strings.ToLower(getenv(d, RerunEnv)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func markRefreshed(d Deps) {
	path := getenv(d, MarkerEnv)
	if path == "" {
		return
	}
	write := d.WriteMarker
	if write == nil {
		write = writeMarkerFile
	}
	if err := write(path); err != nil {
		d.Log.Warnf("could not write %s: %v", path, err)
	}
}

func writeMarkerFile(path string) error {
	stamp := strconv.FormatInt(time.Now().Unix(), 10) + "\n"
	if err := os.WriteFile(path, []byte(stamp), 0o600); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}
	return nil
}

func lookup(d Deps, name, token string) (negcache.Entry, bool) {
	if d.Cache == nil {
		return negcache.Entry{}, false
	}
	return d.Cache.Lookup(name, token)
}

// record stores a fruitless vault read; failing to do so only costs a prompt.
func record(d Deps, name, token string, reason negcache.Reason) {
	if d.Cache == nil {
		return
	}
	if err := d.Cache.Record(name, token, reason); err != nil {
		d.Log.Warnf("%v", err)
	}
}

func packageManager(d Deps) string {
	if bin := getenv(d, BinEnv); bin != "" {
		return bin
	}
	return PackageManager
}

func getenv(d Deps, key string) string {
	if d.Getenv == nil {
		return os.Getenv(key)
	}
	return d.Getenv(key)
}

// quote renders args so the printed command survives a paste back into a shell.
func quote(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, quoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

func quoteArg(arg string) string {
	if arg != "" && !strings.ContainsFunc(arg, needsQuoting) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func needsQuoting(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	}
	return !strings.ContainsRune("@%+=:,./-_", r)
}
