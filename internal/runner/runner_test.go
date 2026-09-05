package runner_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frodi-karlsson/pnop/internal/runner"
)

func TestRunReportsZeroOnSuccess(t *testing.T) {
	res, err := runner.Exec{}.Run(t.Context(), "true")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Code != 0 {
		t.Errorf("code = %d, want 0", res.Code)
	}
}

func TestRunReportsExitCodeWithoutError(t *testing.T) {
	// A failing command is a result, not an error: the caller decides.
	res, err := runner.Exec{}.Run(t.Context(), "sh", "-c", "exit 17")
	if err != nil {
		t.Fatalf("Run returned an error for a non-zero exit: %v", err)
	}
	if res.Code != 17 {
		t.Errorf("code = %d, want 17", res.Code)
	}
}

// A killed process reports ExitCode() == -1, which would exit pnop with 255 and
// look like an ordinary auth failure. It must map to the shell's 128+signum.
func TestRunMapsSignalDeathTo128PlusSignal(t *testing.T) {
	res, err := runner.Exec{}.Run(t.Context(), "sh", "-c", "kill -9 $$")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := 128 + 9; res.Code != want {
		t.Errorf("code = %d, want %d", res.Code, want)
	}
	if !runner.Signalled(res.Code) {
		t.Errorf("Signalled(%d) = false, want true", res.Code)
	}
}

func TestSignalled(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{0, false},
		{1, false},
		{17, false},
		{128, false},
		{137, true}, // SIGKILL
		{130, true}, // SIGINT
	}

	for _, tt := range tests {
		if got := runner.Signalled(tt.code); got != tt.want {
			t.Errorf("Signalled(%d) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestRunErrorsWhenBinaryIsMissing(t *testing.T) {
	_, err := runner.Exec{}.Run(t.Context(), "pnop-definitely-not-a-real-binary")
	if err == nil {
		t.Fatal("Run succeeded, want an error for a missing binary")
	}
}

// pnop decides whether a failure was a credential problem by reading what the
// child printed, so the output has to be captured as well as displayed.
func TestRunCapturesOutputWhileStillDisplayingIt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r := runner.Exec{Stdout: &stdout, Stderr: &stderr}

	res, err := r.Run(t.Context(), "sh", "-c", `echo to-stdout; echo to-stderr >&2; exit 1`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Code != 1 {
		t.Errorf("code = %d, want 1", res.Code)
	}
	if !strings.Contains(stdout.String(), "to-stdout") {
		t.Errorf("stdout = %q, want the child's output to reach the caller", stdout.String())
	}
	if !strings.Contains(stderr.String(), "to-stderr") {
		t.Errorf("stderr = %q, want the child's stderr to reach the caller", stderr.String())
	}
	// Both streams matter: pnpm prints its fetch errors on stdout, but npm and
	// other tools use stderr, so neither can be dropped.
	if !strings.Contains(res.Output, "to-stdout") {
		t.Errorf("captured = %q, want it to include stdout", res.Output)
	}
	if !strings.Contains(res.Output, "to-stderr") {
		t.Errorf("captured = %q, want it to include stderr", res.Output)
	}
}

// A very long install must not grow pnop's memory without bound. Only the tail
// is kept, which is where pnpm prints its error anyway.
func TestRunCapsCapturedOutput(t *testing.T) {
	var stdout bytes.Buffer
	r := runner.Exec{Stdout: &stdout}

	// ~200 KiB, comfortably past the 64 KiB cap.
	res, err := r.Run(t.Context(), "sh", "-c", `i=0; while [ $i -lt 2000 ]; do printf '%0100d\n' $i; i=$((i+1)); done; echo THE-ERROR-LINE; exit 1`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(res.Output) > 128<<10 {
		t.Errorf("captured %d bytes, want the tail to be capped", len(res.Output))
	}
	if !strings.Contains(res.Output, "THE-ERROR-LINE") {
		t.Error("the tail was dropped; the error line must survive truncation")
	}
	if !strings.Contains(stdout.String(), "THE-ERROR-LINE") {
		t.Error("the user's own output was truncated, which must never happen")
	}
}

// The child must receive the caller's stdin so interactive prompts (such as
// corepack's "install pnpm x.y.z? [Y/n]") reach the user instead of being
// swallowed by pnop.
func TestRunInheritsStdinSoPromptsWork(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r := runner.Exec{
		Stdin:  strings.NewReader("Y\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	}

	res, err := r.Run(t.Context(), "sh", "-c", `printf 'prompt [Y/n] '; read ans; echo "got:$ans"; echo "err" >&2`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Code != 0 {
		t.Fatalf("code = %d, want 0", res.Code)
	}

	if got := stdout.String(); !strings.Contains(got, "prompt [Y/n]") {
		t.Errorf("stdout = %q, want the prompt to reach the caller", got)
	}
	if got := stdout.String(); !strings.Contains(got, "got:Y") {
		t.Errorf("stdout = %q, want the child to have read stdin", got)
	}
	if got := stderr.String(); !strings.Contains(got, "err") {
		t.Errorf("stderr = %q, want the child's stderr to reach the caller", got)
	}
}

func TestOutputCapturesStdout(t *testing.T) {
	out, code, err := runner.Exec{}.Output(t.Context(), "sh", "-c", "echo 11.22.0")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if out != "11.22.0" {
		t.Errorf("out = %q, want 11.22.0 (trimmed)", out)
	}
}

func TestOutputReportsExitCode(t *testing.T) {
	_, code, err := runner.Exec{}.Output(t.Context(), "sh", "-c", "exit 3")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if code != 3 {
		t.Errorf("code = %d, want 3", code)
	}
}
