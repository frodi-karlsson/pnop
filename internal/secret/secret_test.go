package secret_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frodi-karlsson/pnop/internal/secret"
)

func TestFetchReturnsTheTrimmedOutput(t *testing.T) {
	got, err := secret.Shell{}.Fetch(t.Context(), `echo "  npm_abc123  "`)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != "npm_abc123" {
		t.Errorf("token = %q, want npm_abc123", got)
	}
}

// The command comes from a human, so it has to behave the way it does when
// typed: quoting, pipes and secret references included.
func TestFetchRunsThroughAShell(t *testing.T) {
	got, err := secret.Shell{}.Fetch(t.Context(), `printf '%s\n' "op://Vault/item/field" | tr '/' '-'`)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != "op:--Vault-item-field" {
		t.Errorf("token = %q, want the piped result", got)
	}
}

// stderr belongs to the terminal so a command can explain itself, or prompt.
func TestFetchLetsTheCommandWriteToStderr(t *testing.T) {
	var stderr bytes.Buffer

	_, err := secret.Shell{Stderr: &stderr}.Fetch(t.Context(), `echo "enter your password" >&2; echo tok`)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(stderr.String(), "enter your password") {
		t.Errorf("stderr = %q, want the command's own output", stderr.String())
	}
}

func TestFetchReportsAMissingProgram(t *testing.T) {
	_, err := secret.Shell{}.Fetch(t.Context(), "definitely-not-a-real-binary read something")

	if err == nil {
		t.Fatal("Fetch succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "definitely-not-a-real-binary") || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want it to name the missing program", err)
	}
}

func TestFetchReportsAFailingCommand(t *testing.T) {
	_, err := secret.Shell{}.Fetch(t.Context(), `echo "not signed in" >&2; exit 1`)

	if err == nil {
		t.Fatal("Fetch succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "token command failed") {
		t.Errorf("err = %v, want it to report the failure", err)
	}
}

// An empty token would be written to the npmrc as an empty token, which reads
// as "no credential" and fails later, further from the cause.
func TestFetchRejectsEmptyOutput(t *testing.T) {
	_, err := secret.Shell{}.Fetch(t.Context(), "true")

	if err == nil || !strings.Contains(err.Error(), "printed nothing") {
		t.Fatalf("err = %v, want it to say the command printed nothing", err)
	}
}

func TestFetchRejectsAnEmptyCommand(t *testing.T) {
	_, err := secret.Shell{}.Fetch(t.Context(), "   ")

	if err == nil || !strings.Contains(err.Error(), "no token command configured") {
		t.Fatalf("err = %v, want it to name the missing configuration", err)
	}
}
