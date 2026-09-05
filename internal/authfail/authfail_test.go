package authfail_test

import (
	"testing"

	"github.com/frodi-karlsson/pnop/internal/authfail"
)

func TestDetectsRealPnpmAuthFailures(t *testing.T) {
	// Captured verbatim from pnpm 11 with a stale token against a private
	// package. Note it is a 404, not a 401.
	const stale = `[ERR_PNPM_FETCH_404] GET https://registry.npmjs.org/@scope%2Fpkg: Not Found - 404

@scope/pkg is not in the npm registry, or you have no permission to fetch it.

An authorization header was used: Bearer npm_[hidden]`

	if !authfail.Detected(stale) {
		t.Error("did not detect a real stale-token failure")
	}
}

func TestDetects(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{"fetch 401", "[ERR_PNPM_FETCH_401] GET https://registry.npmjs.org/x: Unauthorized"},
		{"fetch 403", "[ERR_PNPM_FETCH_403] Forbidden"},
		{"fetch 404", "[ERR_PNPM_FETCH_404] Not Found - 404"},
		{"auth header line alone", "An authorization header was used: Bearer npm_[hidden]"},
		{"needauth", "npm ERR! code ENEEDAUTH"},
		{"bare 401", "401 Unauthorized"},
		{"case insensitive", "err_pnpm_fetch_401"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !authfail.Detected(tt.output) {
				t.Errorf("Detected(%q) = false, want true", tt.output)
			}
		})
	}
}

// These are the failures that must stay silent. Treating any of them as an
// auth problem is what made a failing test suite raise a 1Password prompt.
func TestIgnoresOrdinaryFailures(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{"empty", ""},
		{"failing test suite", "FAIL  src/foo.test.ts\n  ● adds numbers\n    expected 3, got 4\n\nTests: 1 failed"},
		{"lifecycle failure", "$ exit 1\n[ELIFECYCLE] Test failed. See above for more details."},
		{"typescript error", "src/x.ts(3,7): error TS2322: Type 'string' is not assignable to type 'number'."},
		{"missing script", "[ERR_PNPM_NO_SCRIPT] Missing script: nope"},
		{"lockfile drift", "[ERR_PNPM_OUTDATED_LOCKFILE] Cannot install with frozen-lockfile"},
		{"network, not auth", "[ERR_PNPM_FETCH_500] Internal Server Error"},
		{"eslint failure", "/src/a.ts\n  1:1  error  Unexpected console statement  no-console"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if authfail.Detected(tt.output) {
				t.Errorf("Detected(%q) = true, want false", tt.output)
			}
		})
	}
}
