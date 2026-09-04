package setup_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

type savedConfig struct {
	path string
	cfg  config.Entry
	err  error
	n    int
}

func (s *savedConfig) save(path string, cfg config.Entry) error {
	if s.err != nil {
		return s.err
	}
	s.path, s.cfg = path, cfg
	s.n++
	return nil
}

func deps(t *testing.T, sec *fakeSecret, n *fakeNpmrc, saved *savedConfig) setup.Deps {
	t.Helper()
	return setup.Deps{
		ConfigPath: filepath.Join(t.TempDir(), "config.toml"),
		Secret:     sec,
		Npmrc:      n,
		SaveConfig: saved.save,
		Log:        logger.Discard(),
	}
}

func TestWritesTokenAndConfig(t *testing.T) {
	sec := &fakeSecret{token: "npm_tok"}
	n := &fakeNpmrc{}
	saved := &savedConfig{}
	d := deps(t, sec, n, saved)

	err := setup.Run(t.Context(), d, config.Entry{
		File: "/tmp/pnop-test/.npmrc", Vault: "MyVault", Item: "MyItem", Field: "tokenfield",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if n.token != "npm_tok" {
		t.Errorf("wrote token %q, want npm_tok", n.token)
	}
	if n.path != "/tmp/pnop-test/.npmrc" {
		t.Errorf("wrote to %q, want /tmp/pnop-test/.npmrc", n.path)
	}
	if n.registry != "registry.npmjs.org" {
		t.Errorf("registry = %q, want the default", n.registry)
	}
	if saved.n != 1 || saved.path != d.ConfigPath {
		t.Errorf("saved config %d times at %q, want 1 at %q", saved.n, saved.path, d.ConfigPath)
	}
}

func TestAppliesDefaults(t *testing.T) {
	sec := &fakeSecret{token: "npm_tok"}
	saved := &savedConfig{}

	err := setup.Run(t.Context(), deps(t, sec, &fakeNpmrc{}, saved), config.Entry{
		File: "/tmp/.npmrc", Vault: "MyVault", Item: "tok", Field: "tokenfield",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if sec.field != "tokenfield" {
		t.Errorf("fetched field %q, want the field the caller asked for", sec.field)
	}
	if saved.cfg.Registry != "registry.npmjs.org" {
		t.Errorf("saved registry %q, want the default", saved.cfg.Registry)
	}
}

func TestExpandsTildeBeforeSaving(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	saved := &savedConfig{}

	err = setup.Run(t.Context(), deps(t, &fakeSecret{token: "tok"}, &fakeNpmrc{}, saved), config.Entry{
		File: "~/.npmrc.job", Vault: "MyVault", Item: "tok", Field: "tokenfield",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := filepath.Join(home, ".npmrc.job")
	if saved.cfg.File != want {
		t.Errorf("saved file = %q, want %q", saved.cfg.File, want)
	}
	if strings.Contains(saved.cfg.File, "~") {
		t.Error("saved config still contains a tilde")
	}
}

// pnop makes no assumptions about the user's 1Password layout, so vault, item
// and field must be supplied explicitly (unlike File, which has a default).
func TestRequiresEveryItemCoordinate(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Entry
	}{
		{"no vault", config.Entry{File: "/tmp/.npmrc", Item: "tok", Field: "tokenfield"}},
		{"no item", config.Entry{File: "/tmp/.npmrc", Vault: "MyVault", Field: "tokenfield"}},
		{"no field", config.Entry{File: "/tmp/.npmrc", Vault: "MyVault", Item: "tok"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saved := &savedConfig{}
			n := &fakeNpmrc{}

			if err := setup.Run(t.Context(), deps(t, &fakeSecret{token: "t"}, n, saved), tt.cfg); err == nil {
				t.Fatal("Run succeeded, want a validation error")
			}
			if n.writes != 0 || saved.n != 0 {
				t.Error("Run wrote something despite failing validation")
			}
		})
	}
}

func TestDoesNotSaveConfigWhenFetchFails(t *testing.T) {
	sec := &fakeSecret{err: errors.New("op: not signed in")}
	saved := &savedConfig{}
	n := &fakeNpmrc{}

	err := setup.Run(t.Context(), deps(t, sec, n, saved), config.Entry{
		File: "/tmp/.npmrc", Vault: "MyVault", Item: "tok", Field: "tokenfield",
	})

	if err == nil {
		t.Fatal("Run succeeded, want the 1Password error")
	}
	if saved.n != 0 {
		t.Error("saved a config pointing at an item it could not read")
	}
	if n.writes != 0 {
		t.Error("wrote the npmrc despite the fetch failing")
	}
}

func TestDoesNotSaveConfigWhenNpmrcWriteFails(t *testing.T) {
	saved := &savedConfig{}
	n := &fakeNpmrc{writeErr: errors.New("permission denied")}

	err := setup.Run(t.Context(), deps(t, &fakeSecret{token: "tok"}, n, saved), config.Entry{
		File: "/tmp/.npmrc", Vault: "MyVault", Item: "tok", Field: "tokenfield",
	})

	if err == nil {
		t.Fatal("Run succeeded, want the write error")
	}
	if saved.n != 0 {
		t.Error("saved a config despite the npmrc write failing")
	}
}
