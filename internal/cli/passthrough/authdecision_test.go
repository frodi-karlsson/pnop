package passthrough_test

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/pnop/internal/cli/passthrough"
	"github.com/frodi-karlsson/pnop/internal/logger"
)

// Whether pnop reaches for the vault is decided by what pnpm printed, never by
// the command line. These are the cases an earlier argv-based version got
// wrong, and each one is named for the shape of command that broke it.
func TestRecoveryIsDecidedByOutputNotArgv(t *testing.T) {
	const testFailure = "$ exit 1\n[ELIFECYCLE] Test failed. See above for more details."
	const typeError = "src/x.ts(3,7): error TS2322: Type 'string' is not assignable to type 'number'."

	tests := []struct {
		name        string
		args        []string
		output      string
		wantRecover bool
	}{
		{
			// The original complaint: a red test suite must be silent.
			name: "failing test suite", args: []string{"test"},
			output: testFailure, wantRecover: false,
		},
		{
			// pnpm runs any package.json script as a bare subcommand, so this
			// is indistinguishable from an unknown pnpm command by argv alone.
			name: "failing project script", args: []string{"typecheck"},
			output: typeError, wantRecover: false,
		},
		{
			// A script that wraps a registry command. argv says "upd", which
			// tells you nothing; the output says everything.
			name: "script wrapping pnpm update", args: []string{"upd"},
			output: authFailureOutput, wantRecover: true,
		},
		{
			// The subcommand is hidden behind a flag value, so argv parsing
			// read "web" and skipped a genuine registry failure.
			name: "subcommand behind a flag value", args: []string{"--filter", "web", "add", "lodash"},
			output: authFailureOutput, wantRecover: true,
		},
		{
			name: "plain install with a stale token", args: []string{"install"},
			output: authFailureOutput, wantRecover: true,
		},
		{
			// Not every fetch error is an auth problem.
			name: "registry outage", args: []string{"install"},
			output: "[ERR_PNPM_FETCH_500] Internal Server Error", wantRecover: false,
		},
		{
			name: "lockfile drift", args: []string{"install", "--frozen-lockfile"},
			output: "[ERR_PNPM_OUTDATED_LOCKFILE] Cannot install with frozen-lockfile", wantRecover: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &fakeRunner{codes: []int{1, 0}, outputs: []string{tt.output, ""}}
			s := &fakeSecret{token: freshToken}
			n := &fakeNpmrc{token: staleToken}
			var log strings.Builder

			err := passthrough.Run(t.Context(), deps(r, s, n, logger.New(&log)), tt.args)

			if tt.wantRecover {
				if err != nil {
					t.Fatalf("Run: %v", err)
				}
				if s.calls != 1 {
					t.Errorf("hit 1Password %d times, want 1", s.calls)
				}
				if len(r.calls) != 2 {
					t.Errorf("ran pnpm %d times, want 2 (rerun after refresh)", len(r.calls))
				}
				return
			}

			assertExitCode(t, err, 1)
			if s.calls != 0 {
				t.Errorf("hit 1Password %d times, want 0: no auth failure was reported", s.calls)
			}
			if len(r.calls) != 1 {
				t.Errorf("ran pnpm %d times, want 1 (no rerun)", len(r.calls))
			}
			if len(n.writes) != 0 {
				t.Errorf("wrote the npmrc %d times, want 0", len(n.writes))
			}
			if log.String() != "" {
				t.Errorf("logged %q, want silence so the real failure stands alone", log.String())
			}
		})
	}
}

// Output the runner could not capture must not be read as "no auth problem"
// in a way that hides a real one; it simply means there is nothing to go on.
func TestEmptyOutputSkipsRecovery(t *testing.T) {
	r := &fakeRunner{codes: []int{1}, outputs: []string{""}}
	s := &fakeSecret{token: freshToken}

	err := passthrough.Run(t.Context(), deps(r, s, &fakeNpmrc{token: staleToken}, logger.Discard()), []string{"install"})

	assertExitCode(t, err, 1)
	if s.calls != 0 {
		t.Errorf("hit 1Password %d times, want 0 with nothing to inspect", s.calls)
	}
}
