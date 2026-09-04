package passthrough_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frodi-karlsson/pnop/internal/cli"
	"github.com/frodi-karlsson/pnop/internal/cli/passthrough"
	"github.com/frodi-karlsson/pnop/internal/config"
	"github.com/frodi-karlsson/pnop/internal/logger"
)

const (
	staleToken = "stale-token"
	freshToken = "fresh-token"
)

// fakeRunner records every invocation and replays a scripted list of results.
type fakeRunner struct {
	codes []int
	err   error
	calls [][]string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (int, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.err != nil {
		return 0, f.err
	}
	if len(f.calls) > len(f.codes) {
		return 0, nil
	}
	return f.codes[len(f.calls)-1], nil
}

func (f *fakeRunner) Output(_ context.Context, _ string, _ ...string) (string, int, error) {
	return "", 0, nil
}

type fakeSecret struct {
	token string
	err   error
	calls int
}

func (f *fakeSecret) Fetch(_ context.Context, _, _, _ string) (string, error) {
	f.calls++
	return f.token, f.err
}

// fakeNpmrc holds the token in memory rather than on disk.
type fakeNpmrc struct {
	token     string
	readErr   error
	writeErr  error
	writes    []string
	readCalls int
}

func (f *fakeNpmrc) ReadToken(_, _ string) (string, error) {
	f.readCalls++
	return f.token, f.readErr
}

func (f *fakeNpmrc) WriteToken(_, _, token string) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.writes = append(f.writes, token)
	f.token = token
	return nil
}

func deps(r *fakeRunner, s *fakeSecret, n *fakeNpmrc, log logger.Logger) passthrough.Deps {
	return passthrough.Deps{
		Entry: config.Entry{
			File: "/tmp/.npmrc", Vault: "MyVault", Item: "MyItem", Field: "tokenfield",
		}.WithDefaults(),
		Secret: s,
		Npmrc:  n,
		Runner: r,
		Log:    log,
	}
}

func TestSucceedsFirstTryWithoutTouchingOnePassword(t *testing.T) {
	r := &fakeRunner{codes: []int{0}}
	s := &fakeSecret{token: freshToken}
	n := &fakeNpmrc{token: staleToken}

	if err := passthrough.Run(t.Context(), deps(r, s, n, logger.Discard()), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(r.calls) != 1 {
		t.Errorf("ran pnpm %d times, want 1", len(r.calls))
	}
	if s.calls != 0 {
		t.Errorf("fetched from 1Password %d times, want 0 on the happy path", s.calls)
	}
	if len(n.writes) != 0 {
		t.Errorf("wrote npmrc %d times, want 0", len(n.writes))
	}
}

// argv reaches pnpm untouched - no subcommand is injected.
func TestForwardsArgvVerbatim(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"install", []string{"install"}, []string{"pnpm", "install"}},
		{"install with flags", []string{"install", "--frozen-lockfile"}, []string{"pnpm", "install", "--frozen-lockfile"}},
		{"up", []string{"up", "--latest"}, []string{"pnpm", "up", "--latest"}},
		{"run script", []string{"run", "build"}, []string{"pnpm", "run", "build"}},
		{"publish", []string{"publish", "--dry-run"}, []string{"pnpm", "publish", "--dry-run"}},
		{"no args", nil, []string{"pnpm"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &fakeRunner{codes: []int{0}}

			if err := passthrough.Run(t.Context(), deps(r, &fakeSecret{}, &fakeNpmrc{}, logger.Discard()), tt.args); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := r.calls[0]; !equal(got, tt.want) {
				t.Errorf("invocation = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCurrentTokenExposesOriginalFailure(t *testing.T) {
	r := &fakeRunner{codes: []int{17}}
	s := &fakeSecret{token: freshToken}
	n := &fakeNpmrc{token: freshToken} // already matches 1Password
	var log strings.Builder

	err := passthrough.Run(t.Context(), deps(r, s, n, logger.New(&log)), nil)

	assertExitCode(t, err, 17)
	if len(r.calls) != 1 {
		t.Errorf("ran pnpm %d times, want 1 (no retry when the token is current)", len(r.calls))
	}
	if len(n.writes) != 0 {
		t.Errorf("wrote npmrc %d times, want 0", len(n.writes))
	}
	if !strings.Contains(log.String(), "already current") {
		t.Errorf("log = %q, want it to explain the token was current", log.String())
	}
}

func TestStaleTokenIsRefreshedAndInstallRetried(t *testing.T) {
	r := &fakeRunner{codes: []int{17, 0}}
	s := &fakeSecret{token: freshToken}
	n := &fakeNpmrc{token: staleToken}

	if err := passthrough.Run(t.Context(), deps(r, s, n, logger.Discard()), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(r.calls) != 2 {
		t.Fatalf("ran pnpm %d times, want 2", len(r.calls))
	}
	if len(n.writes) != 1 || n.writes[0] != freshToken {
		t.Errorf("npmrc writes = %v, want [%s]", n.writes, freshToken)
	}
}

// Recovery is not limited to install: any failing command gets it.
func TestRecoversForAnyCommand(t *testing.T) {
	for _, cmd := range []string{"install", "up", "add", "publish"} {
		t.Run(cmd, func(t *testing.T) {
			r := &fakeRunner{codes: []int{1, 0}}
			s := &fakeSecret{token: freshToken}
			n := &fakeNpmrc{token: staleToken}

			if err := passthrough.Run(t.Context(), deps(r, s, n, logger.Discard()), []string{cmd}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(r.calls) != 2 {
				t.Errorf("ran pnpm %d times, want 2 (retry after refresh)", len(r.calls))
			}
			if len(n.writes) != 1 {
				t.Errorf("npmrc writes = %v, want one", n.writes)
			}
		})
	}
}

// The token is a credential: it must never reach pnop's own output, on any path.
func TestTokenIsNeverLogged(t *testing.T) {
	var log strings.Builder
	r := &fakeRunner{codes: []int{17, 0}}
	s := &fakeSecret{token: freshToken}
	n := &fakeNpmrc{token: staleToken}

	if err := passthrough.Run(t.Context(), deps(r, s, n, logger.New(&log)), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Contains(log.String(), freshToken) {
		t.Errorf("log leaked the fresh token: %q", log.String())
	}
	if strings.Contains(log.String(), staleToken) {
		t.Errorf("log leaked the previous token: %q", log.String())
	}
}

// A killed pnpm says nothing about credentials, so pnop must not prompt
// 1Password or retry the install.
func TestSignalledFailureSkipsTheTokenCheck(t *testing.T) {
	r := &fakeRunner{codes: []int{137}} // SIGKILL
	s := &fakeSecret{token: freshToken}
	n := &fakeNpmrc{token: staleToken}

	err := passthrough.Run(t.Context(), deps(r, s, n, logger.Discard()), nil)

	assertExitCode(t, err, 137)
	if s.calls != 0 {
		t.Errorf("fetched from 1Password %d times, want 0 for a killed process", s.calls)
	}
	if len(r.calls) != 1 {
		t.Errorf("ran pnpm %d times, want 1 (no retry)", len(r.calls))
	}
}

func TestRetryFailureReportsSecondExitCode(t *testing.T) {
	r := &fakeRunner{codes: []int{17, 9}}
	s := &fakeSecret{token: freshToken}
	n := &fakeNpmrc{token: staleToken}

	err := passthrough.Run(t.Context(), deps(r, s, n, logger.Discard()), nil)

	assertExitCode(t, err, 9)
}

func TestMissingNpmrcCountsAsStale(t *testing.T) {
	r := &fakeRunner{codes: []int{17, 0}}
	s := &fakeSecret{token: freshToken}
	n := &fakeNpmrc{token: ""} // no npmrc entry yet

	if err := passthrough.Run(t.Context(), deps(r, s, n, logger.Discard()), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(n.writes) != 1 {
		t.Errorf("npmrc writes = %v, want the fresh token to be written", n.writes)
	}
}

func TestOnePasswordFailureKeepsOriginalExitCode(t *testing.T) {
	r := &fakeRunner{codes: []int{17}}
	s := &fakeSecret{err: errors.New("op: not signed in")}
	n := &fakeNpmrc{token: staleToken}
	var log strings.Builder

	err := passthrough.Run(t.Context(), deps(r, s, n, logger.New(&log)), nil)

	assertExitCode(t, err, 17)
	if len(r.calls) != 1 {
		t.Errorf("ran pnpm %d times, want 1", len(r.calls))
	}
	if !strings.Contains(log.String(), "not signed in") {
		t.Errorf("log = %q, want it to surface the 1Password error", log.String())
	}
}

func TestNpmrcWriteFailureKeepsOriginalExitCode(t *testing.T) {
	r := &fakeRunner{codes: []int{17}}
	s := &fakeSecret{token: freshToken}
	n := &fakeNpmrc{token: staleToken, writeErr: errors.New("permission denied")}

	err := passthrough.Run(t.Context(), deps(r, s, n, logger.Discard()), nil)

	assertExitCode(t, err, 17)
	if len(r.calls) != 1 {
		t.Errorf("ran pnpm %d times, want 1 (no retry when the write failed)", len(r.calls))
	}
}

func TestNpmrcReadFailureKeepsOriginalExitCode(t *testing.T) {
	r := &fakeRunner{codes: []int{17}}
	s := &fakeSecret{token: freshToken}
	n := &fakeNpmrc{readErr: errors.New("permission denied")}

	err := passthrough.Run(t.Context(), deps(r, s, n, logger.Discard()), nil)

	assertExitCode(t, err, 17)
	if s.calls != 0 {
		t.Errorf("fetched from 1Password %d times, want 0 when the npmrc is unreadable", s.calls)
	}
}

func TestPnpmMissingIsAnError(t *testing.T) {
	r := &fakeRunner{err: errors.New("executable file not found")}

	err := passthrough.Run(t.Context(), deps(r, &fakeSecret{}, &fakeNpmrc{}, logger.Discard()), nil)

	if err == nil {
		t.Fatal("Run succeeded, want an error")
	}
	var exitErr *cli.ExitError
	if errors.As(err, &exitErr) {
		t.Errorf("err = %v, want a plain error rather than an exit code", err)
	}
}

func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()
	var exitErr *cli.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v, want *cli.ExitError", err)
	}
	if exitErr.Code != want {
		t.Errorf("exit code = %d, want %d", exitErr.Code, want)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
