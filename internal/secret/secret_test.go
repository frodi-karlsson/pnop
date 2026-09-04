package secret_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/pni/internal/secret"
)

// stubOP writes a fake `op` executable and returns its path.
func stubOP(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "op")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

func TestFetchReturnsTheTrimmedField(t *testing.T) {
	op := secret.OP{Bin: stubOP(t, `echo "npm_abc123"`)}

	got, err := op.Fetch(t.Context(), "MyVault", "item", "tokenfield")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != "npm_abc123" {
		t.Errorf("token = %q, want npm_abc123", got)
	}
}

func TestFetchPassesTheItemCoordinates(t *testing.T) {
	// Echo the args back so the test can assert how op was invoked.
	op := secret.OP{Bin: stubOP(t, `echo "$@"`)}

	got, err := op.Fetch(t.Context(), "My Vault", "My Item", "otherfield")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	for _, want := range []string{"item get", "My Item", "--vault My Vault", "--fields label=otherfield", "--reveal"} {
		if !strings.Contains(got, want) {
			t.Errorf("invocation %q is missing %q", got, want)
		}
	}
}

func TestFetchRejectsAnEmptyField(t *testing.T) {
	op := secret.OP{Bin: stubOP(t, `echo ""`)}

	if _, err := op.Fetch(t.Context(), "MyVault", "item", "tokenfield"); err == nil {
		t.Error("Fetch succeeded on an empty field, want an error")
	}
}

func TestFetchSurfacesOPFailure(t *testing.T) {
	var stderr bytes.Buffer
	op := secret.OP{
		Bin:    stubOP(t, `echo "not signed in" >&2; exit 1`),
		Stderr: &stderr,
	}

	_, err := op.Fetch(t.Context(), "MyVault", "item", "tokenfield")
	if err == nil {
		t.Fatal("Fetch succeeded, want an error")
	}
	// The item and vault help the user see which reference failed.
	if !strings.Contains(err.Error(), "item") {
		t.Errorf("err = %v, want it to name the item", err)
	}
	if !strings.Contains(stderr.String(), "not signed in") {
		t.Errorf("stderr = %q, want op's own diagnostics passed through", stderr.String())
	}
}

func TestFetchReportsAMissingOPBinary(t *testing.T) {
	op := secret.OP{Bin: "pni-definitely-not-a-real-op"}

	_, err := op.Fetch(t.Context(), "MyVault", "item", "tokenfield")
	if err == nil {
		t.Fatal("Fetch succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "1Password CLI not found") {
		t.Errorf("err = %v, want a clear 'not found' message", err)
	}
}
