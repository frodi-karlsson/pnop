package setup_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/frodi-karlsson/pnop/internal/cli/setup"
	"github.com/frodi-karlsson/pnop/internal/config"
	"github.com/frodi-karlsson/pnop/internal/logger"
)

type fakeSecret struct {
	token string
	err   error
	vault string
	item  string
	field string
}

func (f *fakeSecret) Fetch(_ context.Context, vault, item, field string) (string, error) {
	f.vault, f.item, f.field = vault, item, field
	return f.token, f.err
}

type fakeNpmrc struct {
	path     string
	registry string
	token    string
	writeErr error
	writes   int
}

func (f *fakeNpmrc) ReadToken(_, _ string) (string, error) { return f.token, nil }

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

// `pnop setup -c private` with no other flags is a pure profile switch.
func TestActivatesAnExistingConfig(t *testing.T) {
	store := &stubStore{cfg: config.Config{
		Active: "job",
		Configs: map[string]config.Entry{
			"job":     {File: "/tmp/.npmrc", Vault: "V", Item: "I", Field: "F", Registry: "registry.npmjs.org"},
			"private": {File: "/tmp/.npmrc", Vault: "E", Item: "P", Field: "password", Registry: "registry.npmjs.org"},
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
	if sec.item != "P" {
		t.Errorf("fetched item %q, want P", sec.item)
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
		Vault: "V", Item: "I", Field: "F",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if store.saved.Active != "job" {
		t.Errorf("Active = %q, want job", store.saved.Active)
	}
	entry := store.saved.Configs["job"]
	if entry.Vault != "V" || entry.Item != "I" || entry.Field != "F" {
		t.Errorf("saved entry = %+v, want V/I/F", entry)
	}
	if entry.Registry != "registry.npmjs.org" {
		t.Errorf("Registry = %q, want the default", entry.Registry)
	}
}

// Activating a name that does not exist, with no flags to create it, must fail
// before anything is written.
func TestActivatingAnUnknownConfigFails(t *testing.T) {
	store := &stubStore{cfg: config.Config{
		Configs: map[string]config.Entry{"job": {File: "/tmp/.npmrc", Vault: "V", Item: "I", Field: "F"}},
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
func TestUpdatesAnExistingConfig(t *testing.T) {
	store := &stubStore{cfg: config.Config{
		Active: "job",
		Configs: map[string]config.Entry{
			"job":     {File: "/tmp/.npmrc", Vault: "V", Item: "I", Field: "F", Registry: "registry.npmjs.org"},
			"private": {File: "/tmp/.npmrc", Vault: "E", Item: "P", Field: "password", Registry: "registry.npmjs.org"},
		},
	}}

	err := setup.Run(t.Context(), deps(t, &fakeSecret{token: "t"}, &fakeNpmrc{}, store), "job", config.Entry{
		Vault: "V2", Item: "I2", Field: "F2",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := store.saved.Configs["job"].Vault; got != "V2" {
		t.Errorf("job vault = %q, want V2", got)
	}
	if got := store.saved.Configs["private"].Vault; got != "E" {
		t.Errorf("private vault = %q, want it untouched", got)
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

	err := setup.Run(t.Context(), deps(t, sec, n, store), "job", config.Entry{Vault: "V", Item: "I", Field: "F"})

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
		Vault: "V", Item: "I", Field: "F",
	})

	if err == nil {
		t.Fatal("Run succeeded, want the write error")
	}
	if store.saveN != 0 {
		t.Error("saved a config despite the npmrc write failing")
	}
}
