// Command pnop wraps `pnpm install` and transparently recovers from an npm
// auth token that has gone stale, refreshing it from 1Password and retrying.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/frodi-karlsson/pnop/internal/cli"
	"github.com/frodi-karlsson/pnop/internal/cli/install"
	"github.com/frodi-karlsson/pnop/internal/cli/setup"
	"github.com/frodi-karlsson/pnop/internal/config"
	"github.com/frodi-karlsson/pnop/internal/logger"
	"github.com/frodi-karlsson/pnop/internal/npmrc"
	"github.com/frodi-karlsson/pnop/internal/runner"
	"github.com/frodi-karlsson/pnop/internal/secret"
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
		Use:     "pnop [pnpm args...]",
		Short:   "pnpm install, with automatic npm token recovery",
		Version: version.Version,
		Long: "pnop runs `pnpm install`. If that fails, it compares the npm token in your\n" +
			"managed npmrc against 1Password; when the token is stale it refreshes the\n" +
			"file and retries, and when it is already current it leaves the original\n" +
			"failure alone.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		// Errors are reported once, by exitCode, so cobra must not also print them.
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bare `pnop` is `pnop install`; flag parsing is off so that pnpm's
			// own flags pass straight through.
			if len(args) > 0 {
				switch args[0] {
				case "-h", "--help", "help":
					return cmd.Help()
				case "-v", "--version":
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), version.Version)
					return nil
				}
			}
			deps, err := installDeps()
			if err != nil {
				return err
			}
			return install.Run(cmd.Context(), deps, args)
		},
	}

	root.AddCommand(setup.Command(setupDeps))
	root.AddCommand(install.Command(installDeps))
	return root
}

func installDeps() (install.Deps, error) {
	path, err := config.Path()
	if err != nil {
		return install.Deps{}, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return install.Deps{}, err
	}

	return install.Deps{
		Config: cfg.WithDefaults(),
		Secret: secret.OP{Stdin: os.Stdin, Stderr: os.Stderr},
		Npmrc:  npmrc.FileStore{},
		Runner: runner.Exec{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr},
		Log:    logger.New(os.Stderr),
	}, nil
}

func setupDeps() (setup.Deps, error) {
	path, err := config.Path()
	if err != nil {
		return setup.Deps{}, err
	}

	return setup.Deps{
		ConfigPath: path,
		Secret:     secret.OP{Stdin: os.Stdin, Stderr: os.Stderr},
		Npmrc:      npmrc.FileStore{},
		SaveConfig: config.Save,
		Log:        logger.New(os.Stderr),
	}, nil
}

// exitCode maps an error to a process exit status, preserving pnpm's own code
// so callers and CI see what pnpm actually reported.
func exitCode(err error) int {
	var exitErr *cli.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	fmt.Fprintf(os.Stderr, "pnop: %v\n", err)
	return 1
}
