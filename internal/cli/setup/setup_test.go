package setup_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/pnop/internal/cli/setup"
	"github.com/frodi-karlsson/pnop/internal/config"
	"github.com/frodi-karlsson/pnop/internal/logger"
	"github.com/frodi-karlsson/pnop/internal/verify"
)

type fakeSecret struct {
	token   string
	err     error
	command string
	calls   int
}

func (f *fakeSecret) Fetch(_ context.Context, command string) (string, error) {
	f.command = command
	f.calls++
	return f.token, f.err
}

type fakeNpmrc struct {
	path     string
	registry string
	token    string
	writeErr error
	writes   int
	named    string // empty means it agrees with whatever the config manages
}

func (f *fakeNpmrc) ReadToken(_, _ string) (string, error) { return f.token, nil }

func (f *fakeNpmrc) ReadRegistry(_ string) (string, error) {
	if f.named == "" {
		return f.registry, nil
	}
	return f.named, nil
}

// fakeIdentifier answers for the token setup just fetched.
type fakeIdentifier struct {
	user    string
	outcome verify.Outcome
	tokens  []string
}

func (f *fakeIdentifier) Verify(ctx context.Context, registry, token string) verify.Outcome {
	_, outcome := f.Identify(ctx, registry, token)
	return outcome
}

func (f *fakeIdentifier) Identify(_ context.Context, _, token string) (string, verify.Outcome) {
	f.tokens = append(f.tokens, token)
	return f.user, f.outcome
}

func (f *fakeNpmrc) WriteToken(path, registry, token string) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.path, f.registry, f.token = path, registry, token
	f.writes++
	return nil
}

// stubStore stands in for the on-disk config document.
type stubStore struct {
	cfg     config.Config
	loadErr error
	saved   config.Config
	saveErr error
	saveN   int
}

func (s *stubStore) load(string) (config.Config, error) { return s.cfg, s.loadErr }

func (s *stubStore) save(_ string, cfg config.Config) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = cfg
	s.saveN++
	return nil
}

func deps(t *testing.T, sec *fakeSecret, n *fakeNpmrc, store *stubStore) setup.Deps {
	t.Helper()
	return setup.Deps{
		ConfigPath: filepath.Join(t.TempDir(), "config.toml"),
		Secret:     sec,
		Npmrc:      n,
		LoadConfig: store.load,
		SaveConfig: store.save,
		Log:        logger.Discard(),
	}
}

// The ordinary case: the npmrc agrees, so setup stays silent.
func agreeing() *fakeNpmrc { return &fakeNpmrc{named: "registry.npmjs.org"} }

// `pnop setup -c private` with no other flags is a pure profile switch.
func TestActivatesAnExistingConfig(t *testing.T) {
	store := &stubStore{cfg: config.Config{
		Active: "job",
		Configs: map[string]config.Entry{
			"job":     {File: "/tmp/.npmrc", Command: "print I", Registry: "registry.npmjs.org"},
			"private": {File: "/tmp/.npmrc", Command: "print P", Registry: "registry.npmjs.org"},
		},
	}}
	sec := &fakeSecret{token: "private_tok"}
	n := &fakeNpmrc{}

	if err := setup.Run(t.Context(), deps(t, sec, n, store), "private", config.Entry{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if store.saved.Active != "private" {
		t.Errorf("Active = %q, want private", store.saved.Active)
	}
	if sec.command != "print P" {
		t.Errorf("ran %q, want the config's command", sec.command)
	}
	if n.token != "private_tok" {
		t.Errorf("wrote token %q, want private_tok", n.token)
	}
}

// Flags create the entry when it does not exist yet, then activate it.
func TestCreatesThenActivates(t *testing.T) {
	store := &stubStore{cfg: config.Config{}, loadErr: config.ErrNotConfigured}
	sec := &fakeSecret{token: "tok"}

	err := setup.Run(t.Context(), deps(t, sec, &fakeNpmrc{}, store), "job", config.Entry{
		Command: "print I",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if store.saved.Active != "job" {
		t.Errorf("Active = %q, want job", store.saved.Active)
	}
	entry := store.saved.Configs["job"]
	if entry.Command != "print I" {
		t.Errorf("saved command = %q, want the one that was passed", entry.Command)
	}
	if entry.Registry != "registry.npmjs.org" {
		t.Errorf("Registry = %q, want the default", entry.Registry)
	}
}

// Activating a name that does not exist, with no flags to create it, must fail
// before anything is written.
func TestActivatingAnUnknownConfigFails(t *testing.T) {
	store := &stubStore{cfg: config.Config{
		Configs: map[string]config.Entry{"job": {File: "/tmp/.npmrc", Command: "print I"}},
	}}
	n := &fakeNpmrc{}

	err := setup.Run(t.Context(), deps(t, &fakeSecret{token: "t"}, n, store), "nope", config.Entry{})

	if err == nil {
		t.Fatal("Run succeeded, want an error")
	}
	if store.saveN != 0 || n.writes != 0 {
		t.Error("Run wrote something despite the config being unknown")
	}
}

// Flags on an existing entry update it in place, leaving siblings alone.
func TestFlagsReplaceTheWholeConfig(t *testing.T) {
	store := &stubStore{cfg: config.Config{
		Active: "job",
		Configs: map[string]config.Entry{
			"job":     {File: "/tmp/.npmrc", Command: "print I", Registry: "registry.npmjs.org"},
			"private": {File: "/tmp/.npmrc", Command: "print P", Registry: "registry.npmjs.org"},
		},
	}}

	err := setup.Run(t.Context(), deps(t, &fakeSecret{token: "t"}, agreeing(), store), "job", config.Entry{
		Command: "print I2", File: "/tmp/.npmrc",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The omitted registry returns to its default rather than keeping the
	// stored one: that is how an optional field is cleared.
	want := config.Entry{File: "/tmp/.npmrc", Command: "print I2", Registry: "registry.npmjs.org"}
	if got := store.saved.Configs["job"]; got != want {
		t.Errorf("job = %+v, want %+v", got, want)
	}
	if got := store.saved.Configs["private"].Command; got != "print P" {
		t.Errorf("private command = %q, want it untouched", got)
	}
}

// Replacing means a config cannot quietly inherit what the flags left out, so
// an entry with no command fails before anything is fetched or written.
func TestReplacingWithoutACommandIsRefused(t *testing.T) {
	store := &stubStore{cfg: config.Config{
		Active:  "job",
		Configs: map[string]config.Entry{"job": {File: "/tmp/.npmrc", Command: "print I"}},
	}}
	sec := &fakeSecret{token: "t"}
	n := agreeing()

	err := setup.Run(t.Context(), deps(t, sec, n, store), "job", config.Entry{File: "/tmp/other.npmrc"})

	if err == nil {
		t.Fatal("Run succeeded, want an error naming what is missing")
	}
	if !strings.Contains(err.Error(), "command is required") || !strings.Contains(err.Error(), "whole config") {
		t.Errorf("err = %v, want it to name the missing field and explain replacement", err)
	}
	if sec.calls != 0 || n.writes != 0 || store.saveN != 0 {
		t.Errorf("fetched %d, wrote %d, saved %d - want nothing to happen", sec.calls, n.writes, store.saveN)
	}
}

// An optional field the flags cannot express must not survive a replacement.
func TestReplacingClearsTheRerunOptIn(t *testing.T) {
	store := &stubStore{cfg: config.Config{
		Active: "job",
		Configs: map[string]config.Entry{
			"job": {File: "/tmp/.npmrc", Command: "print I", Registry: "registry.npmjs.org", Rerun: true},
		},
	}}

	err := setup.Run(t.Context(), deps(t, &fakeSecret{token: "t"}, agreeing(), store), "job", config.Entry{
		File: "/tmp/.npmrc", Command: "print I",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if store.saved.Configs["job"].Rerun {
		t.Error("rerun survived a replacement, want it cleared like any omitted field")
	}
}

// A pure switch keeps everything, including what the flags cannot express.
func TestSwitchingKeepsTheStoredEntry(t *testing.T) {
	stored := config.Entry{File: "/tmp/.npmrc", Command: "print I", Registry: "npm.pkg.github.com", Rerun: true}
	store := &stubStore{cfg: config.Config{Active: "other", Configs: map[string]config.Entry{"job": stored}}}

	err := setup.Run(t.Context(), deps(t, &fakeSecret{token: "t"}, &fakeNpmrc{named: "npm.pkg.github.com"}, store), "job", config.Entry{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := store.saved.Configs["job"]; got != stored {
		t.Errorf("job = %+v, want it untouched %+v", got, stored)
	}
}

func TestRequiresAConfigName(t *testing.T) {
	store := &stubStore{}

	if err := setup.Run(t.Context(), deps(t, &fakeSecret{}, &fakeNpmrc{}, store), "", config.Entry{}); err == nil {
		t.Error("Run succeeded with no -c, want an error")
	}
}

func TestDoesNotSaveConfigWhenFetchFails(t *testing.T) {
	store := &stubStore{cfg: config.Config{}, loadErr: config.ErrNotConfigured}
	sec := &fakeSecret{err: errors.New("op: not signed in")}
	n := &fakeNpmrc{}

	err := setup.Run(t.Context(), deps(t, sec, n, store), "job", config.Entry{Command: "print I"})

	if err == nil {
		t.Fatal("Run succeeded, want the 1Password error")
	}
	if store.saveN != 0 {
		t.Error("saved a config pointing at an item it could not read")
	}
	if n.writes != 0 {
		t.Error("wrote the npmrc despite the fetch failing")
	}
}

func TestDoesNotSaveConfigWhenNpmrcWriteFails(t *testing.T) {
	store := &stubStore{cfg: config.Config{}, loadErr: config.ErrNotConfigured}
	n := &fakeNpmrc{writeErr: errors.New("permission denied")}

	err := setup.Run(t.Context(), deps(t, &fakeSecret{token: "tok"}, n, store), "job", config.Entry{
		Command: "print I",
	})

	if err == nil {
		t.Fatal("Run succeeded, want the write error")
	}
	if store.saveN != 0 {
		t.Error("saved a config despite the npmrc write failing")
	}
}

// The identity is reported in the present tense, never as a promise.
func TestReportsIdentityWithoutPromisingLongevity(t *testing.T) {
	store := &stubStore{cfg: config.Config{}, loadErr: config.ErrNotConfigured}
	id := &fakeIdentifier{user: "frodi", outcome: verify.Valid}
	var log strings.Builder

	d := deps(t, &fakeSecret{token: "tok"}, agreeing(), store)
	d.Identifier = id
	d.Log = logger.New(&log)

	err := setup.Run(t.Context(), d, "job", config.Entry{
		File: "/tmp/.npmrc", Command: "print I",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(id.tokens) != 1 || id.tokens[0] != "tok" {
		t.Errorf("probed %v, want the fetched token once", id.tokens)
	}
	if !strings.Contains(log.String(), "accepts this token right now, as frodi") {
		t.Errorf("log = %q, want the identity in the present tense", log.String())
	}
	if !strings.Contains(log.String(), "granular access token") {
		t.Errorf("log = %q, want the granular-token warning when a config is created", log.String())
	}
	if strings.Contains(log.String(), "tok") && !strings.Contains(log.String(), "token") {
		t.Errorf("log = %q, leaked the token", log.String())
	}
}

// A rejected token is still written, but not silently.
func TestWarnsWhenTheFetchedTokenIsRejected(t *testing.T) {
	store := &stubStore{cfg: config.Config{}, loadErr: config.ErrNotConfigured}
	n := agreeing()
	var log strings.Builder

	d := deps(t, &fakeSecret{token: "dead"}, n, store)
	d.Identifier = &fakeIdentifier{outcome: verify.Rejected}
	d.Log = logger.New(&log)

	err := setup.Run(t.Context(), d, "job", config.Entry{
		File: "/tmp/.npmrc", Command: "print I",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if n.writes != 1 {
		t.Errorf("npmrc writes = %d, want 1: setup writes what it was told to", n.writes)
	}
	if !strings.Contains(log.String(), "rejects the token") {
		t.Errorf("log = %q, want a warning about the rejected token", log.String())
	}
}

// A disagreement means pnop would probe a host the command never touched.
func TestWarnsWhenTheNpmrcNamesAnotherRegistry(t *testing.T) {
	tests := []struct {
		name     string
		named    string
		wantWarn bool
	}{
		{"npmrc agrees", "registry.npmjs.org", false},
		{"npmrc has no registry line", "registry.npmjs.org", false},
		{"npmrc points elsewhere", "npm.pkg.github.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &stubStore{cfg: config.Config{}, loadErr: config.ErrNotConfigured}
			var log strings.Builder

			d := deps(t, &fakeSecret{token: "tok"}, &fakeNpmrc{named: tt.named}, store)
			d.Log = logger.New(&log)

			err := setup.Run(t.Context(), d, "job", config.Entry{
				File: "/tmp/.npmrc", Command: "print I",
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			warned := strings.Contains(log.String(), "sets registry=")
			if warned != tt.wantWarn {
				t.Errorf("warned = %v, want %v (log = %q)", warned, tt.wantWarn, log.String())
			}
		})
	}
}

func TestRemoveDeletesTheConfig(t *testing.T) {
	store := &stubStore{cfg: config.Config{
		Active: "work",
		Configs: map[string]config.Entry{
			"work":     {File: "/tmp/.npmrc", Command: "print I", Registry: "registry.npmjs.org"},
			"personal": {File: "/tmp/.npmrc", Command: "print P", Registry: "registry.npmjs.org"},
		},
	}}
	n := agreeing()

	if err := setup.Remove(deps(t, &fakeSecret{}, n, store), "personal", config.Entry{}); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, ok := store.saved.Configs["personal"]; ok {
		t.Error("config was still saved after removal")
	}
	if store.saved.Active != "work" {
		t.Errorf("Active = %q, want work: removing another config must not deactivate it", store.saved.Active)
	}
	if n.writes != 0 {
		t.Errorf("npmrc writes = %d, want 0: removing a profile says nothing about the token on disk", n.writes)
	}
}

// Removing the active config leaves pnop with nothing to refresh, which it has
// to say rather than fail silently on the next command.
func TestRemoveTheActiveConfigClearsIt(t *testing.T) {
	store := &stubStore{cfg: config.Config{
		Active:  "work",
		Configs: map[string]config.Entry{"work": {File: "/tmp/.npmrc", Command: "print I"}},
	}}
	var log strings.Builder
	d := deps(t, &fakeSecret{}, agreeing(), store)
	d.Log = logger.New(&log)

	if err := setup.Remove(d, "work", config.Entry{}); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if store.saved.Active != "" {
		t.Errorf("Active = %q, want it cleared", store.saved.Active)
	}
	if !strings.Contains(log.String(), "no config is active") {
		t.Errorf("log = %q, want it to say nothing is active", log.String())
	}
}

func TestRemoveRejectsUnknownAndCombinedFlags(t *testing.T) {
	base := config.Config{
		Active:  "work",
		Configs: map[string]config.Entry{"work": {File: "/tmp/.npmrc", Command: "print I"}},
	}

	tests := []struct {
		name  string
		cfg   string
		flags config.Entry
	}{
		{"unknown config", "nope", config.Entry{}},
		{"combined with other flags", "work", config.Entry{Vault: "V"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &stubStore{cfg: base}

			err := setup.Remove(deps(t, &fakeSecret{}, agreeing(), store), tt.cfg, tt.flags)

			if err == nil {
				t.Fatal("Remove succeeded, want an error")
			}
			if store.saveN != 0 {
				t.Errorf("saved the config %d times, want 0", store.saveN)
			}
		})
	}
}

// An old config keeps working, and says so once, with the line that replaces it.
func TestWarnsAboutADeprecatedConfig(t *testing.T) {
	store := &stubStore{cfg: config.Config{
		Active: "job",
		Configs: map[string]config.Entry{
			"job": {File: "/tmp/.npmrc", Vault: "RnD", Item: "NPM token", Field: "password"},
		},
	}}
	var log strings.Builder
	d := deps(t, &fakeSecret{token: "tok"}, agreeing(), store)
	d.Log = logger.New(&log)

	if err := setup.Run(t.Context(), d, "job", config.Entry{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(log.String(), "deprecated") {
		t.Errorf("log = %q, want a deprecation warning", log.String())
	}
	if !strings.Contains(log.String(), "--command") {
		t.Errorf("log = %q, want the replacement command line", log.String())
	}
	if store.saved.Configs["job"].Command == "" {
		t.Error("the migrated command was not saved")
	}
}

func TestSaysNothingAboutACurrentConfig(t *testing.T) {
	store := &stubStore{cfg: config.Config{
		Active:  "job",
		Configs: map[string]config.Entry{"job": {File: "/tmp/.npmrc", Command: "print token"}},
	}}
	var log strings.Builder
	d := deps(t, &fakeSecret{token: "tok"}, agreeing(), store)
	d.Log = logger.New(&log)

	if err := setup.Run(t.Context(), d, "job", config.Entry{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Contains(log.String(), "deprecated") {
		t.Errorf("log = %q, want no deprecation warning", log.String())
	}
}
