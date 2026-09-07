// Command pnop wraps pnpm and transparently recovers from an npm auth token
// that has gone stale, refreshing it from 1Password and rerunning the command.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/frodi-karlsson/pnop/internal/cli"
	"github.com/frodi-karlsson/pnop/internal/cli/passthrough"
	"github.com/frodi-karlsson/pnop/internal/cli/refresh"
	"github.com/frodi-karlsson/pnop/internal/cli/setup"
	"github.com/frodi-karlsson/pnop/internal/config"
	"github.com/frodi-karlsson/pnop/internal/logger"
	"github.com/frodi-karlsson/pnop/internal/negcache"
	"github.com/frodi-karlsson/pnop/internal/npmrc"
	"github.com/frodi-karlsson/pnop/internal/runner"
	"github.com/frodi-karlsson/pnop/internal/secret"
	"github.com/frodi-karlsson/pnop/internal/verify"
	"github.com/frodi-karlsson/pnop/internal/version"
	"github.com/spf13/cobra"
)

func main() {
	root := newRoot()

	if err := root.ExecuteContext(context.Background()); err != nil {
		os.Exit(exitCode(err))
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "pnop [pnpm args...]",
		Short: "pnpm, with automatic npm token recovery",
		Long: "pnop forwards every command to pnpm. If a command fails, it asks the\n" +
			"registry whether your npm token is still accepted, and refreshes it from\n" +
			"1Password when it is not.\n\n" +
			"pnop's own commands carry a `+`: +setup, +refresh, +version, +help.\n" +
			"Anything without it is pnpm's, including `setup`, `help` and `--version`,\n" +
			"which pnpm defines itself.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		// Errors are reported once, by exitCode, so cobra must not also print them.
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bare `pnop` has nothing to forward, so it introduces itself.
			if len(args) == 0 {
				return cmd.Help()
			}
			return passthrough.Run(cmd.Context(), passthroughDeps(), args)
		},
	}

	// cobra injects a `help` command by default, and pnop must let `help` reach pnpm.
	root.SetHelpCommand(&cobra.Command{Hidden: true, Use: "no-op-help"})

	root.AddCommand(setup.Command(setupDeps))
	root.AddCommand(refresh.Command(setupDeps))
	root.AddCommand(versionCommand())
	root.AddCommand(helpCommand(root))
	return root
}

// versionCommand reports pnop's version. `--version` belongs to pnpm.
func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "+version",
		Short:        "Print pnop's version and the pnpm it drives",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printVersions(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

// helpCommand exists because `help` and `--help` reach pnpm now.
func helpCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:          "+help",
		Short:        "Show pnop's own help",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return root.Help()
		},
	}
}

// printVersions reports pnop's own version and the pnpm it will drive.
func printVersions(ctx context.Context, out io.Writer) error {
	_, _ = fmt.Fprintf(out, "pnop %s\n", version.Version)

	pnpm := passthrough.PackageManager
	if override := os.Getenv(passthrough.BinEnv); override != "" {
		pnpm = override
	}
	pnpmVersion, code, err := execRunner().Output(ctx, pnpm, "--version")
	if err != nil || code != 0 {
		_, _ = fmt.Fprintln(out, "pnpm not found on PATH")
		return nil
	}
	_, _ = fmt.Fprintf(out, "pnpm %s\n", pnpmVersion)
	return nil
}

func execRunner() runner.Exec {
	return runner.Exec{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
}

func passthroughDeps() passthrough.Deps {
	log := logger.New(os.Stderr)
	return passthrough.Deps{
		LoadEntry: loadActiveEntry,
		Secret:    secret.OP{Stdin: os.Stdin, Stderr: os.Stderr},
		Npmrc:     npmrc.FileStore{},
		Runner:    execRunner(),
		Verifier:  verify.HTTP{Log: log},
		Cache:     probeCache(log),
		Log:       log,
	}
}

// probeCache locates the negative cache, or returns nil when the platform will
// not say where per-user cache files belong. A missing cache costs repeated
// 1Password reads on a failing registry, which is not worth refusing to run over.
func probeCache(log logger.Logger) negcache.Cache {
	dir, err := negcache.Default()
	if err != nil {
		log.Warnf("%v", err)
		return nil
	}
	return dir
}

// loadActiveEntry resolves the config the active profile points at, and its
// name, which keys the negative cache. It is deliberately not called until a
// pnpm command has failed.
func loadActiveEntry() (string, config.Entry, error) {
	path, err := config.Path()
	if err != nil {
		return "", config.Entry{}, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", config.Entry{}, err
	}
	entry, err := cfg.ActiveEntry()
	return cfg.Active, entry, err
}

func setupDeps() (setup.Deps, error) {
	path, err := config.Path()
	if err != nil {
		return setup.Deps{}, err
	}

	log := logger.New(os.Stderr)
	return setup.Deps{
		ConfigPath: path,
		Secret:     secret.OP{Stdin: os.Stdin, Stderr: os.Stderr},
		Npmrc:      npmrc.FileStore{},
		Identifier: verify.HTTP{Log: log},
		LoadConfig: config.Load,
		SaveConfig: config.Save,
		Log:        log,
	}, nil
}

// exitCode maps an error to a process exit status, preserving pnpm's own code
// so callers and CI see what pnpm actually reported.
func exitCode(err error) int {
	var exitErr *cli.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	fmt.Fprintf(os.Stderr, "[pnop] [error] %v\n", err)
	return 1
}
