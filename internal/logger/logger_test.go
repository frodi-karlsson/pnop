package logger_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/frodi-karlsson/pni/internal/logger"
)

func TestInfofPrefixesAndTerminates(t *testing.T) {
	var sb strings.Builder
	logger.New(&sb).Infof("wrote %s", "/tmp/.npmrc")

	if want := "pni: wrote /tmp/.npmrc\n"; sb.String() != want {
		t.Errorf("output = %q, want %q", sb.String(), want)
	}
}

func TestWarnfPrefixesAndTerminates(t *testing.T) {
	var sb strings.Builder
	logger.New(&sb).Warnf("%v", errors.New("not signed in"))

	if want := "pni: not signed in\n"; sb.String() != want {
		t.Errorf("output = %q, want %q", sb.String(), want)
	}
}

func TestDoesNotDoubleUpNewlines(t *testing.T) {
	var sb strings.Builder
	logger.New(&sb).Infof("already newline-terminated\n")

	if want := "pni: already newline-terminated\n"; sb.String() != want {
		t.Errorf("output = %q, want %q", sb.String(), want)
	}
}

func TestMultipleLinesAccumulate(t *testing.T) {
	var sb strings.Builder
	l := logger.New(&sb)
	l.Infof("first")
	l.Warnf("second")

	if want := "pni: first\npni: second\n"; sb.String() != want {
		t.Errorf("output = %q, want %q", sb.String(), want)
	}
}

func TestDiscardProducesNothing(t *testing.T) {
	// Compiles and runs without panicking; nothing to observe by design.
	logger.Discard().Infof("ignored %d", 1)
	logger.Discard().Warnf("ignored")
}

func TestNilWriterIsSafe(t *testing.T) {
	logger.New(nil).Infof("must not panic")
}

func TestWriteErrorIsSwallowed(t *testing.T) {
	// Progress output is best-effort: a broken pipe must not surface.
	logger.New(failingWriter{}).Infof("boom")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
