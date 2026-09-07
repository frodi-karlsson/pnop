// Package secret runs the command that prints an npm token.
package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// notFound is the exit status a shell uses for a command it cannot find.
const notFound = 127

// Fetcher retrieves a token by running a command.
type Fetcher interface {
	Fetch(ctx context.Context, command string) (string, error)
}

// Shell runs the configured command through `sh -c` and takes its stdout as
// the token. A shell rather than a bare exec because the command comes from a
// human: `op read "op://Vault/item/field"` should behave the way it does when
// typed, quoting and all.
type Shell struct {
	// Stdin and Stderr are wired to the terminal so the command can prompt for
	// a biometric unlock. Only stdout is captured, since that carries the token.
	Stdin  io.Reader
	Stderr io.Writer
}

// Fetch runs command and returns its trimmed stdout.
func (s Shell) Fetch(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("no token command configured - run: pnop +setup -c <name> --command '<command>'")
	}

	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin = s.Stdin
	cmd.Stdout = &stdout
	cmd.Stderr = s.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == notFound {
			return "", fmt.Errorf("%s: command not found - it is what this config's --command runs", first(command))
		}
		return "", fmt.Errorf("token command failed (%s): %w", first(command), err)
	}

	token := strings.TrimSpace(stdout.String())
	if token == "" {
		return "", fmt.Errorf("the token command printed nothing: %s", command)
	}
	return token, nil
}

// first names the program a command runs, for an error that points at the
// missing tool rather than at the whole command line.
func first(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return command
	}
	return fields[0]
}
