package logger_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/frodi-karlsson/pnop/internal/logger"
)

func TestInfofPrefixesAndTerminates(t *testing.T) {
	var sb strings.Builder
	logger.New(&sb).Infof("wrote %s", "/tmp/.npmrc")

	if want := "[pnop] wrote /tmp/.npmrc\n"; sb.String() != want {
		t.Errorf("output = %q, want %q", sb.String(), want)
	}
}

func TestWarnfPrefixesAndTerminates(t *testing.T) {
	var sb strings.Builder
	logger.New(&sb).Warnf("%v", errors.New("not signed in"))

	if want := "[pnop] [warning] not signed in\n"; sb.String() != want {
		t.Errorf("output = %q, want %q", sb.String(), want)
	}
}

func TestDoesNotDoubleUpNewlines(t *testing.T) {
	var sb strings.Builder
	logger.New(&sb).Infof("already newline-terminated\n")

	if want := "[pnop] already newline-terminated\n"; sb.String() != want {
		t.Errorf("output = %q, want %q", sb.String(), want)
	}
}

// A warning has to be tellable from ordinary progress at a glance, otherwise
// a 1Password failure reads the same as a routine status line.
func TestWarningsAreDistinguishableFromProgress(t *testing.T) {
	var info, warn strings.Builder
	logger.New(&info).Infof("same text")
	logger.New(&warn).Warnf("same text")

	if info.String() == warn.String() {
		t.Fatalf("Infof and Warnf render identically: %q", info.String())
	}
	if !strings.Contains(warn.String(), "[warning]") {
		t.Errorf("warning = %q, want it tagged", warn.String())
	}
	if strings.Contains(info.String(), "[warning]") {
		t.Errorf("info = %q, want no warning tag", info.String())
	}
}

func TestMultipleLinesAccumulate(t *testing.T) {
	var sb strings.Builder
	l := logger.New(&sb)
	l.Infof("first")
	l.Warnf("second")

	if want := "[pnop] first\n[pnop] [warning] second\n"; sb.String() != want {
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
