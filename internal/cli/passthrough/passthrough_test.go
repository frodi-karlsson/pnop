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
	"github.com/frodi-karlsson/pnop/internal/negcache"
	"github.com/frodi-karlsson/pnop/internal/runner"
	"github.com/frodi-karlsson/pnop/internal/verify"
)

const (
	staleToken = "stale-token"
	freshToken = "fresh-token"
	configName = "work"
)

// fakeRunner replays scripted exit codes and records each call's extra env.
type fakeRunner struct {
	codes []int
	err   error
	calls [][]string
	envs  [][]string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (runner.Result, error) {
	return f.RunEnv(ctx, nil, name, args...)
}

func (f *fakeRunner) RunEnv(_ context.Context, extraEnv []string, name string, args ...string) (runner.Result, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	f.envs = append(f.envs, extraEnv)
	if f.err != nil {
		return runner.Result{}, f.err
	}
	i := len(f.calls) - 1
	if i >= len(f.codes) {
		return runner.Result{Code: 0}, nil
	}
	return runner.Result{Code: f.codes[i]}, nil
}

func (f *fakeRunner) Output(_ context.Context, _ string, _ ...string) (string, int, error) {
	return "", 0, nil
}

type fakeSecret struct {
	token string
	err   error
	calls int
}

func (f *fakeSecret) Fetch(_ context.Context, _ string) (string, error) {
	f.calls++
	return f.token, f.err
}

// fakeNpmrc holds the token in memory rather than on disk.
type fakeNpmrc struct {
	token     string
	registry  string
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

func (f *fakeNpmrc) ReadRegistry(_ string) (string, error) {
	return f.registry, nil
}

// fakeVerifier answers per token, or by call order when the same token has to
// be answered differently twice. Statuses map to outcomes in verify's own tests.
type fakeVerifier struct {
	outcomes   map[string]verify.Outcome
	byCall     []verify.Outcome
	tokens     []string
	registries []string
}

func (f *fakeVerifier) Verify(_ context.Context, registry, token string) verify.Outcome {
	i := len(f.tokens)
	f.tokens = append(f.tokens, token)
	f.registries = append(f.registries, registry)
	if i < len(f.byCall) {
		return f.byCall[i]
	}
	if outcome, ok := f.outcomes[token]; ok {
		return outcome
	}
	return verify.Inconclusive
}

func (f *fakeVerifier) calls() int { return len(f.tokens) }

type fakeCache struct {
	entry    negcache.Entry
	hit      bool
	recorded []negcache.Reason
	keys     []string
	err      error
}

func (f *fakeCache) Lookup(cfg, token string) (negcache.Entry, bool) {
	f.keys = append(f.keys, cfg+"/"+token)
	return f.entry, f.hit
}

func (f *fakeCache) Record(cfg, token string, reason negcache.Reason) error {
	f.keys = append(f.keys, cfg+"/"+token)
	f.recorded = append(f.recorded, reason)
	return f.err
}

// harness collects the fakes so a test can assert on any of them after Run.
type harness struct {
	runner   *fakeRunner
	secret   *fakeSecret
	npmrc    *fakeNpmrc
	verifier *fakeVerifier
	cache    *fakeCache
	env      map[string]string
	markers  []string
	log      strings.Builder
	entry    config.Entry
	loaded   bool
}

func newHarness(codes []int, disk string, outcomes map[string]verify.Outcome) *harness {
	return &harness{
		runner:   &fakeRunner{codes: codes},
		secret:   &fakeSecret{token: freshToken},
		npmrc:    &fakeNpmrc{token: disk},
		verifier: &fakeVerifier{outcomes: outcomes},
		cache:    &fakeCache{},
		env:      map[string]string{},
		entry:    config.Entry{File: "/tmp/.npmrc", Command: "print-token"}.WithDefaults(),
	}
}

func (h *harness) deps() passthrough.Deps {
	return passthrough.Deps{
		LoadEntry: func() (string, config.Entry, error) {
			h.loaded = true
			return configName, h.entry, nil
		},
		Secret:      h.secret,
		Npmrc:       h.npmrc,
		Runner:      h.runner,
		Verifier:    h.verifier,
		Cache:       h.cache,
		Getenv:      func(key string) string { return h.env[key] },
		WriteMarker: func(path string) error { h.markers = append(h.markers, path); return nil },
		Log:         logger.New(&h.log),
	}
}

func (h *harness) run(t *testing.T, args ...string) error {
	t.Helper()
	return passthrough.Run(t.Context(), h.deps(), args)
}

// rejected is the only starting point that justifies a vault read.
func rejected() map[string]verify.Outcome {
	return map[string]verify.Outcome{staleToken: verify.Rejected}
}

func TestSucceedsFirstTryWithoutProbingOrPrompting(t *testing.T) {
	h := newHarness([]int{0}, staleToken, rejected())

	if err := h.run(t); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(h.runner.calls) != 1 {
		t.Errorf("ran pnpm %d times, want 1", len(h.runner.calls))
	}
	if h.verifier.calls() != 0 {
		t.Errorf("probed the registry %d times, want 0 on the happy path", h.verifier.calls())
	}
	if h.secret.calls != 0 {
		t.Errorf("fetched from 1Password %d times, want 0 on the happy path", h.secret.calls)
	}
	if h.loaded { // pnop is a plain pnpm alias until something fails
		t.Error("loaded the config on the success path")
	}
	if h.log.String() != "" {
		t.Errorf("logged %q, want silence", h.log.String())
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
			h := newHarness([]int{0}, staleToken, rejected())

			if err := h.run(t, tt.args...); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := h.runner.calls[0]; !equal(got, tt.want) {
				t.Errorf("invocation = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRejectedTokenIsRefreshedWithoutRerunning(t *testing.T) {
	h := newHarness([]int{17}, staleToken, map[string]verify.Outcome{
		staleToken: verify.Rejected,
		freshToken: verify.Valid,
	})

	err := h.run(t, "install")

	assertExitCode(t, err, 17) // the refresh does not change what pnpm reported
	if len(h.npmrc.writes) != 1 || h.npmrc.writes[0] != freshToken {
		t.Errorf("npmrc writes = %v, want [%s]", h.npmrc.writes, freshToken)
	}
	if len(h.runner.calls) != 1 {
		t.Errorf("ran pnpm %d times, want 1: reruns are opt-in", len(h.runner.calls))
	}
	if !strings.Contains(h.log.String(), "run it again: pnpm install") {
		t.Errorf("log = %q, want the command printed for the user to rerun", h.log.String())
	}
}

// The token is a credential: it must never reach pnop's own output, on any path.
func TestTokenIsNeverLogged(t *testing.T) {
	h := newHarness([]int{17}, staleToken, map[string]verify.Outcome{
		staleToken: verify.Rejected,
		freshToken: verify.Valid,
	})

	_ = h.run(t, "install")

	if strings.Contains(h.log.String(), freshToken) {
		t.Errorf("log leaked the fresh token: %q", h.log.String())
	}
	if strings.Contains(h.log.String(), staleToken) {
		t.Errorf("log leaked the previous token: %q", h.log.String())
	}
}

// A killed pnpm says nothing about credentials.
func TestSignalledFailureSkipsTheGate(t *testing.T) {
	h := newHarness([]int{137}, staleToken, rejected()) // SIGKILL

	err := h.run(t, "install")

	assertExitCode(t, err, 137)
	if h.verifier.calls() != 0 {
		t.Errorf("probed %d times, want 0 for a killed process", h.verifier.calls())
	}
	if h.secret.calls != 0 {
		t.Errorf("fetched from 1Password %d times, want 0 for a killed process", h.secret.calls)
	}
}

func TestOnePasswordFailureKeepsOriginalExitCode(t *testing.T) {
	h := newHarness([]int{17}, staleToken, rejected())
	h.secret.err = errors.New("op: not signed in")
	h.secret.token = ""

	err := h.run(t, "install")

	assertExitCode(t, err, 17)
	if len(h.npmrc.writes) != 0 {
		t.Errorf("npmrc writes = %v, want none", h.npmrc.writes)
	}
	if !strings.Contains(h.log.String(), "not signed in") {
		t.Errorf("log = %q, want it to surface the 1Password error", h.log.String())
	}
}

func TestNpmrcWriteFailureKeepsOriginalExitCode(t *testing.T) {
	h := newHarness([]int{17}, staleToken, map[string]verify.Outcome{
		staleToken: verify.Rejected,
		freshToken: verify.Valid,
	})
	h.npmrc.writeErr = errors.New("permission denied")

	err := h.run(t, "install")

	assertExitCode(t, err, 17)
	if len(h.runner.calls) != 1 {
		t.Errorf("ran pnpm %d times, want 1", len(h.runner.calls))
	}
}

func TestNpmrcReadFailureKeepsOriginalExitCode(t *testing.T) {
	h := newHarness([]int{17}, staleToken, rejected())
	h.npmrc.readErr = errors.New("permission denied")

	err := h.run(t, "install")

	assertExitCode(t, err, 17)
	if h.verifier.calls() != 0 {
		t.Errorf("probed %d times, want 0 when the npmrc is unreadable", h.verifier.calls())
	}
	if h.secret.calls != 0 {
		t.Errorf("fetched from 1Password %d times, want 0 when the npmrc is unreadable", h.secret.calls)
	}
}

func TestPnpmMissingIsAnError(t *testing.T) {
	h := newHarness(nil, staleToken, rejected())
	h.runner.err = errors.New("executable file not found")

	err := h.run(t, "install")

	if err == nil {
		t.Fatal("Run succeeded, want an error")
	}
	var exitErr *cli.ExitError
	if errors.As(err, &exitErr) {
		t.Errorf("err = %v, want a plain error rather than an exit code", err)
	}
}

// An unconfigured pnop must still surface pnpm's own failure, not replace it
// with a configuration error.
func TestUnconfiguredFailurePreservesExitCode(t *testing.T) {
	h := newHarness([]int{17}, staleToken, rejected())
	d := h.deps()
	d.LoadEntry = func() (string, config.Entry, error) {
		return "", config.Entry{}, errors.New("pnop is not configured yet")
	}

	err := passthrough.Run(t.Context(), d, []string{"install"})

	assertExitCode(t, err, 17)
	if h.verifier.calls() != 0 {
		t.Errorf("probed %d times, want 0 when unconfigured", h.verifier.calls())
	}
	if h.secret.calls != 0 {
		t.Errorf("hit 1Password %d times, want 0 when unconfigured", h.secret.calls)
	}
	if !strings.Contains(h.log.String(), "not configured") {
		t.Errorf("log = %q, want it to explain the config problem", h.log.String())
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
