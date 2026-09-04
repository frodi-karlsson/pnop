package runner_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frodi-karlsson/pni/internal/runner"
)

func TestRunReportsZeroOnSuccess(t *testing.T) {
	code, err := runner.Exec{}.Run(t.Context(), "true")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
}

func TestRunReportsExitCodeWithoutError(t *testing.T) {
	// A failing command is a result, not an error: the caller decides.
	code, err := runner.Exec{}.Run(t.Context(), "sh", "-c", "exit 17")
	if err != nil {
		t.Fatalf("Run returned an error for a non-zero exit: %v", err)
	}
	if code != 17 {
		t.Errorf("code = %d, want 17", code)
	}
}

// A killed process reports ExitCode() == -1, which would exit pni with 255 and
// look like an ordinary auth failure. It must map to the shell's 128+signum.
func TestRunMapsSignalDeathTo128PlusSignal(t *testing.T) {
	code, err := runner.Exec{}.Run(t.Context(), "sh", "-c", "kill -9 $$")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := 128 + 9; code != want {
		t.Errorf("code = %d, want %d", code, want)
	}
	if !runner.Signalled(code) {
		t.Errorf("Signalled(%d) = false, want true", code)
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
	_, err := runner.Exec{}.Run(t.Context(), "pni-definitely-not-a-real-binary")
	if err == nil {
		t.Fatal("Run succeeded, want an error for a missing binary")
	}
}

// The child must inherit the caller's streams so interactive prompts (such as
// corepack's "install pnpm x.y.z? [Y/n]") reach the user instead of being
// swallowed by pni.
func TestRunInheritsStdioSoPromptsWork(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r := runner.Exec{
		Stdin:  strings.NewReader("Y\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	}

	code, err := r.Run(t.Context(), "sh", "-c", `printf 'prompt [Y/n] '; read ans; echo "got:$ans"; echo "err" >&2`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
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
