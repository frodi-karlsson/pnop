// Package logger provides pnop's progress output.
//
// Everything here writes to stderr in practice, so that pnop's own chatter
// never contaminates the package manager's stdout.
package logger

import (
	"fmt"
	"io"
)

// Prefix identifies pnop's own lines in amongst pnpm's output.
const Prefix = "pnop: "

// Logger reports progress to the user. It is an interface so commands can be
// tested without producing output.
type Logger interface {
	// Infof reports normal progress.
	Infof(format string, args ...any)
	// Warnf reports something that went wrong but was handled.
	Warnf(format string, args ...any)
}

// writerLogger writes prefixed lines to an io.Writer.
type writerLogger struct {
	w io.Writer
}

// New returns a Logger writing to w. A nil w discards output.
func New(w io.Writer) Logger {
	if w == nil {
		return Discard()
	}
	return writerLogger{w: w}
}

// Discard returns a Logger that swallows everything, for tests and quiet runs.
func Discard() Logger {
	return writerLogger{w: io.Discard}
}

func (l writerLogger) Infof(format string, args ...any) {
	l.write(format, args...)
}

func (l writerLogger) Warnf(format string, args ...any) {
	l.write(format, args...)
}

// write is best-effort: failing to print progress must never mask the exit
// code pnop is trying to report.
func (l writerLogger) write(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if len(msg) == 0 || msg[len(msg)-1] != '\n' {
		msg += "\n"
	}
	_, _ = io.WriteString(l.w, Prefix+msg)
}
