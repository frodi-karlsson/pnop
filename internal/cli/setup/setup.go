// Package setup records credential configs and switches between them.
package setup

import (
	"context"
	"errors"
	"fmt"

	"github.com/frodi-karlsson/pnop/internal/config"
	"github.com/frodi-karlsson/pnop/internal/logger"
	"github.com/frodi-karlsson/pnop/internal/npmrc"
	"github.com/frodi-karlsson/pnop/internal/secret"
	"github.com/frodi-karlsson/pnop/internal/verify"
	"github.com/spf13/cobra"
)

// Deps are the collaborators Run needs, injected for testability.
type Deps struct {
	// ConfigPath is where the config document lives.
	ConfigPath string
	Secret     secret.Fetcher
	Npmrc      npmrc.Store
	Identifier verify.Identifier
	LoadConfig func(path string) (config.Config, error)
	SaveConfig func(path string, cfg config.Config) error
	// Log receives progress messages, never the token itself.
	Log logger.Logger
}

// Command returns the `pnop +setup` subcommand.
func Command(load func() (Deps, error)) *cobra.Command {
	var name string
	var remove bool
	entry := config.Entry{}

	cmd := &cobra.Command{
		Use:   "+setup -c <name>",
		Short: "Switch to a credential config, creating it if flags are given",
		Long: "Activate a named credential config and write its token to the npmrc it\n" +
			"manages. With no flags it is a pure profile switch.\n\n" +
			"Any flag defines the config outright: what you pass is the whole entry,\n" +
			"and what you leave out goes back to its default rather than to whatever\n" +
			"was there before. That is how an optional field is cleared. With --remove\n" +
			"the config is deleted instead, leaving the npmrc alone.\n\n" +
			"The `+` is what separates pnop's commands from pnpm's, which has a\n" +
			"`setup` of its own.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, err := load()
			if err != nil {
				return err
			}
			if remove {
				return Remove(deps, name, entry)
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
	cmd.Flags().BoolVar(&entry.Rerun, "rerun", false, "rerun a failed command once after refreshing its token")
	cmd.Flags().BoolVar(&remove, "remove", false, "delete the named config instead of activating it")

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
		return errors.New("a config name is required: pnop +setup -c <name>")
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

	if err := Apply(ctx, d, entry); err != nil {
		return err
	}

	cfg.Configs[name] = entry
	cfg.Active = name
	if err := d.SaveConfig(d.ConfigPath, cfg); err != nil {
		return err
	}

	d.Log.Infof("active config is now %q", name)
	d.Log.Infof("wrote %s", entry.File)
	if flags != (config.Entry{}) {
		d.Log.Infof("the item should hold a granular access token - `npm login` writes a " +
			"short-lived session token, and an item holding one makes almost every command prompt")
	}
	return nil
}

// report says who the token belongs to, in the present tense: accepted now is
// not accepted in an hour, and every npm token expires.
func report(ctx context.Context, d Deps, entry config.Entry, token string) {
	if d.Identifier == nil {
		return
	}
	switch user, outcome := d.Identifier.Identify(ctx, entry.Registry, token); outcome {
	case verify.Valid:
		if user != "" {
			d.Log.Infof("%s accepts this token right now, as %s", entry.Registry, user)
			return
		}
		d.Log.Infof("%s accepts this token right now", entry.Registry)
	case verify.Rejected:
		d.Log.Warnf("%s rejects the token in this item - writing it anyway, since that is what "+
			"was asked for, but no pnpm command will work until the item holds a live token",
			entry.Registry)
	default:
		d.Log.Infof("could not confirm the token with %s", entry.Registry)
	}
}

// warnRegistryMismatch reports a config managing a registry the npmrc does not
// name, where pnop would be probing a host the failing command never touched.
// A project-level npmrc can still redirect at run time, which setup cannot see.
func warnRegistryMismatch(d Deps, entry config.Entry) {
	named, err := d.Npmrc.ReadRegistry(entry.File)
	if err != nil {
		d.Log.Warnf("%v", err)
		return
	}
	if named == entry.Registry {
		return
	}
	d.Log.Warnf("%s sets registry=%s, but this config manages %s - pnop will check and refresh "+
		"the token for %s only", entry.File, named, entry.Registry, entry.Registry)
}

// Apply fetches the entry's token, reports on it and writes the npmrc. It is
// the half of setup that `pnop +refresh` repeats without touching the config.
func Apply(ctx context.Context, d Deps, entry config.Entry) error {
	token, err := d.Secret.Fetch(ctx, entry.Vault, entry.Item, entry.Field)
	if err != nil {
		return err
	}

	report(ctx, d, entry, npmrc.NormalizeToken(token))

	if err := d.Npmrc.WriteToken(entry.File, entry.Registry, token); err != nil {
		return err
	}
	warnRegistryMismatch(d, entry)
	return nil
}

// Remove deletes the named config. The npmrc is left alone: dropping a profile
// says nothing about whether the credential on disk is still wanted.
func Remove(d Deps, name string, flags config.Entry) error {
	if name == "" {
		return errors.New("a config name is required: pnop +setup -c <name> --remove")
	}
	if flags != (config.Entry{}) {
		return errors.New("--remove takes no other flags")
	}

	cfg, err := d.LoadConfig(d.ConfigPath)
	if err != nil {
		return err
	}
	if _, err := cfg.Entry(name); err != nil {
		return err
	}

	delete(cfg.Configs, name)
	wasActive := cfg.Active == name
	if wasActive {
		cfg.Active = ""
	}
	if err := d.SaveConfig(d.ConfigPath, cfg); err != nil {
		return err
	}

	d.Log.Infof("removed config %q", name)
	if wasActive {
		d.Log.Warnf("no config is active now - run: pnop +setup -c <name>")
	}
	return nil
}

// resolveEntry returns the entry to activate. With no flags that is the stored
// one; with any flag it is the flags themselves, so an omitted optional field
// returns to its default instead of keeping an older value. Merging would make
// a field impossible to clear, and would hide a half-typed command as a
// working one.
func resolveEntry(cfg config.Config, name string, flags config.Entry) (config.Entry, error) {
	entry, known := cfg.Configs[name]
	if flags == (config.Entry{}) {
		if !known {
			return config.Entry{}, &config.UnknownConfigError{Name: name, Known: cfg.Names()}
		}
	} else {
		entry = flags
	}

	entry = entry.WithDefaults()
	if err := entry.Validate(); err != nil {
		return config.Entry{}, fmt.Errorf(
			"%w - pnop +setup defines the whole config, so pass --vault, --item and --field together", err)
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
