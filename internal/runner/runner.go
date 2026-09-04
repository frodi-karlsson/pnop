// Package runner executes the package manager as a child process.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
)

// Runner runs a command and reports its exit code. A non-nil error means the
// command could not be run at all; a failed command reports a non-zero code
// with a nil error.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (int, error)
}

// Exec is the real Runner. Its streams are inherited by the child process, so
// interactive prompts (for instance corepack's "install pnpm x.y.z? [Y/n]")
// reach the user's terminal untouched.
type Exec struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run executes name with args, returning its exit code.
func (e Exec) Run(ctx context.Context, name string, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = e.Stdin
	cmd.Stdout = e.Stdout
	cmd.Stderr = e.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return waitStatusCode(exitErr), nil
		}
		return 0, fmt.Errorf("run %s: %w", name, err)
	}
	return 0, nil
}

// waitStatusCode converts a process result to a shell-style exit code.
// ExitCode reports -1 for a signalled process, so a killed child is mapped to
// the conventional 128+signum rather than surfacing as -1 (which would exit
// pni with 255 and look like an ordinary failure).
func waitStatusCode(exitErr *exec.ExitError) int {
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return exitErr.ExitCode()
}

// Signalled reports whether code denotes a process killed by a signal rather
// than one that chose to exit. Such a failure says nothing about credentials,
// so callers should not treat it as a reason to go looking for a stale token.
func Signalled(code int) bool {
	return code > 128
}
