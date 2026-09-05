// Package runner executes the package manager as a child process.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// tailBytes caps how much of the child's output is retained for inspection.
// Only the end matters: pnpm prints its error last.
const tailBytes = 64 << 10

// Result is the outcome of running a command.
type Result struct {
	// Code is the shell-style exit status.
	Code int
	// Output is what the child printed, capped to the last tailBytes. It is
	// captured so a caller can tell a credential failure from an ordinary one
	// without guessing from the command line.
	Output string
}

// Runner runs a command and reports the result. A non-nil error means the
// command could not be run at all; a failed command reports a non-zero code
// with a nil error.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
	// Output runs the command and captures its stdout, for the rare case
	// where pnop needs to read a value rather than show the user output.
	Output(ctx context.Context, name string, args ...string) (string, int, error)
}

// Exec is the real Runner.
//
// When Stdout is a terminal, the child is given a pseudo-terminal so that it
// still believes it is talking to one: colour, in-place progress rendering and
// interactive prompts all keep working, while pnop reads the same bytes on
// their way to the screen. Without a PTY the child would see a pipe and
// degrade its output, which is why a plain io.MultiWriter is not enough.
//
// When Stdout is not a terminal, as in CI or when piped, there is no terminal
// behaviour left to preserve and the simpler path is used.
type Exec struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run executes name with args, returning its exit status and captured output.
func (e Exec) Run(ctx context.Context, name string, args ...string) (Result, error) {
	if in, out, ok := e.terminal(); ok {
		return e.runOnPTY(ctx, in, out, name, args...)
	}
	return e.runPiped(ctx, name, args...)
}

// terminal reports whether both stdin and stdout are the real terminal, which
// is what makes allocating a PTY worthwhile and safe.
func (e Exec) terminal() (*os.File, *os.File, bool) {
	in, inOK := e.Stdin.(*os.File)
	out, outOK := e.Stdout.(*os.File)
	if !inOK || !outOK {
		return nil, nil, false
	}
	if !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return nil, nil, false
	}
	return in, out, true
}

// runOnPTY runs the child attached to a pseudo-terminal, forwarding bytes both
// ways and keeping a copy of what the child printed.
func (e Exec) runOnPTY(ctx context.Context, in, out *os.File, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		// No PTY available: fall back rather than fail the command outright.
		return e.runPiped(ctx, name, args...)
	}
	defer func() { _ = ptmx.Close() }()

	stopResizing := forwardResizes(ptmx, out)
	defer stopResizing()

	// Raw mode stops the local terminal from line-buffering and echoing, so
	// the child sees each keystroke as it is typed. Without it, a prompt like
	// corepack's "[Y/n]" would not respond until Enter, and would double-echo.
	if state, err := term.MakeRaw(int(in.Fd())); err == nil {
		defer func() { _ = term.Restore(int(in.Fd()), state) }()
	}

	go func() { _, _ = io.Copy(ptmx, in) }()

	tail := &tailBuffer{limit: tailBytes}
	// Read to EOF before Wait so no output is lost. A closed PTY surfaces as
	// EIO on Linux rather than EOF, which is a normal end of stream here.
	_, _ = io.Copy(io.MultiWriter(out, tail), ptmx)

	return result(cmd.Wait(), name, tail.String())
}

// runPiped runs the child with its streams wired straight through, teeing the
// output. Used when there is no terminal to preserve.
func (e Exec) runPiped(ctx context.Context, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	tail := &tailBuffer{limit: tailBytes}

	cmd.Stdin = e.Stdin
	// A nil writer means "discard" for os/exec, but io.MultiWriter would
	// dereference it, so substitute explicitly.
	cmd.Stdout = io.MultiWriter(orDiscard(e.Stdout), tail)
	cmd.Stderr = io.MultiWriter(orDiscard(e.Stderr), tail)

	return result(cmd.Run(), name, tail.String())
}

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// result turns a wait error into a Result, distinguishing "the command failed"
// from "the command could not be run".
func result(err error, name, output string) (Result, error) {
	if err == nil {
		return Result{Code: 0, Output: output}, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return Result{Code: waitStatusCode(exitErr), Output: output}, nil
	}
	return Result{}, fmt.Errorf("run %s: %w", name, err)
}

// forwardResizes keeps the PTY the same size as the real terminal, so pnpm's
// progress rendering does not wrap wrongly when the window changes.
func forwardResizes(ptmx, out *os.File) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)

	resize := func() { _ = pty.InheritSize(out, ptmx) }
	resize() // match the current size before the child draws anything

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				resize()
			case <-done:
				return
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			signal.Stop(ch)
			close(done)
		})
	}
}

// tailBuffer keeps only the last limit bytes written to it, so a very long
// install cannot grow pnop's memory without bound.
type tailBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

// Write implements io.Writer and never fails, so it cannot break the pipeline
// it is teed into.
func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.buf.Write(p)
	if excess := t.buf.Len() - t.limit; excess > 0 {
		t.buf.Next(excess)
	}
	return len(p), nil
}

// String returns the retained tail.
func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

// Output runs name with args and returns its trimmed stdout.
func (e Exec) Output(ctx context.Context, name string, args ...string) (string, int, error) {
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = e.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return strings.TrimSpace(stdout.String()), waitStatusCode(exitErr), nil
		}
		return "", 0, fmt.Errorf("run %s: %w", name, err)
	}
	return strings.TrimSpace(stdout.String()), 0, nil
}

// waitStatusCode converts a process result to a shell-style exit code.
// ExitCode reports -1 for a signalled process, so a killed child is mapped to
// the conventional 128+signum rather than surfacing as -1 (which would exit
// pnop with 255 and look like an ordinary failure).
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
