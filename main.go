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
		Use:   "pnop [pnpm args...]",
		Short: "pnpm, with automatic npm token recovery",
		Long: "pnop forwards every command to pnpm. If a command fails, it compares the\n" +
			"npm token in your active config's npmrc against 1Password; when the token\n" +
			"is stale it refreshes the file and reruns the command, and when it is\n" +
			"already current it leaves the original failure alone.\n\n" +
			"Only `setup`, `--version` and `--help` are pnop's own. Everything else,\n" +
			"including `help`, reaches pnpm untouched.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		// Errors are reported once, by exitCode, so cobra must not also print them.
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bare `pnop` prints help rather than assuming a subcommand.
			if len(args) == 0 {
				return cmd.Help()
			}
			switch args[0] {
			case "-h", "--help":
				return cmd.Help()
			case "-v", "--version":
				return printVersions(cmd.Context(), cmd.OutOrStdout())
			}

			return passthrough.Run(cmd.Context(), passthroughDeps(), args)
		},
	}

	// cobra injects a `help` command by default; pnop must let `help` reach pnpm.
	root.SetHelpCommand(&cobra.Command{Hidden: true, Use: "no-op-help"})

	root.AddCommand(setup.Command(setupDeps))
	return root
}

// printVersions reports pnop's own version and the pnpm it will drive.
func printVersions(ctx context.Context, out io.Writer) error {
	_, _ = fmt.Fprintf(out, "pnop %s\n", version.Version)

	pnpmVersion, code, err := execRunner().Output(ctx, passthrough.PackageManager, "--version")
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
	return passthrough.Deps{
		LoadEntry: loadActiveEntry,
		Secret:    secret.OP{Stdin: os.Stdin, Stderr: os.Stderr},
		Npmrc:     npmrc.FileStore{},
		Runner:    execRunner(),
		Log:       logger.New(os.Stderr),
	}
}

// loadActiveEntry resolves the config the active profile points at. It is
// deliberately not called until a pnpm command has failed.
func loadActiveEntry() (config.Entry, error) {
	path, err := config.Path()
	if err != nil {
		return config.Entry{}, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return config.Entry{}, err
	}
	return cfg.ActiveEntry()
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
		LoadConfig: config.Load,
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
	fmt.Fprintf(os.Stderr, "[pnop] [error] %v\n", err)
	return 1
}
