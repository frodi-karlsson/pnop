package refresh_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/pnop/internal/cli/refresh"
	"github.com/frodi-karlsson/pnop/internal/cli/setup"
	"github.com/frodi-karlsson/pnop/internal/config"
	"github.com/frodi-karlsson/pnop/internal/logger"
)

type fakeSecret struct {
	token   string
	err     error
	command string
	calls   int
}

func (f *fakeSecret) Fetch(_ context.Context, command string) (string, error) {
	f.calls++
	f.command = command
	return f.token, f.err
}

type fakeNpmrc struct {
	token  string
	writes int
}

func (f *fakeNpmrc) ReadToken(_, _ string) (string, error) { return f.token, nil }
func (f *fakeNpmrc) ReadRegistry(_ string) (string, error) { return "registry.npmjs.org", nil }
func (f *fakeNpmrc) WriteToken(_, _, token string) error   { f.token = token; f.writes++; return nil }

func deps(t *testing.T, sec *fakeSecret, n *fakeNpmrc, cfg config.Config, loadErr error) (setup.Deps, *int) {
	t.Helper()
	saves := 0
	for name, entry := range cfg.Configs {
		cfg.Configs[name] = entry.WithDefaults()
	}
	return setup.Deps{
		ConfigPath: filepath.Join(t.TempDir(), "config.toml"),
		Secret:     sec,
		Npmrc:      n,
		LoadConfig: func(string) (config.Config, error) { return cfg, loadErr },
		SaveConfig: func(string, config.Config) error { saves++; return nil },
		Log:        logger.Discard(),
	}, &saves
}

func configured() config.Config {
	return config.Config{
		Active: "work",
		Configs: map[string]config.Entry{
			"work": {File: "/tmp/.npmrc", Command: "work-command", Registry: "registry.npmjs.org"},
		},
	}
}

func TestRefreshesTheActiveConfig(t *testing.T) {
	sec := &fakeSecret{token: "fresh"}
	n := &fakeNpmrc{token: "stale"}
	d, saves := deps(t, sec, n, configured(), nil)

	if err := refresh.Run(t.Context(), d); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if sec.command != "work-command" {
		t.Errorf("ran %q, want the active config's command", sec.command)
	}
	if n.token != "fresh" || n.writes != 1 {
		t.Errorf("npmrc token = %q after %d writes, want fresh after 1", n.token, n.writes)
	}
	if *saves != 0 {
		t.Errorf("saved the config %d times, want 0: refresh changes no configuration", *saves)
	}
}

func TestRefreshWithoutAConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		loadErr error
	}{
		{"never set up", config.Config{}, config.ErrNotConfigured},
		{"nothing active", config.Config{Configs: map[string]config.Entry{"work": {}}}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sec := &fakeSecret{token: "fresh"}
			d, _ := deps(t, sec, &fakeNpmrc{}, tt.cfg, tt.loadErr)

			err := refresh.Run(t.Context(), d)

			if err == nil {
				t.Fatal("Run succeeded, want an error")
			}
			if sec.calls != 0 {
				t.Errorf("hit 1Password %d times, want 0 with nothing to refresh", sec.calls)
			}
		})
	}
}

func TestRefreshSurfacesAVaultFailure(t *testing.T) {
	sec := &fakeSecret{err: errors.New("op: not signed in")}
	n := &fakeNpmrc{token: "stale"}
	d, _ := deps(t, sec, n, configured(), nil)

	err := refresh.Run(t.Context(), d)

	if err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("err = %v, want the 1Password failure", err)
	}
	if n.writes != 0 {
		t.Errorf("npmrc writes = %d, want 0 when nothing was fetched", n.writes)
	}
}

// refresh never saves, so it warns every time until a +setup rewrites the file.
func TestRefreshWarnsAboutADeprecatedConfig(t *testing.T) {
	cfg := config.Config{
		Active: "work",
		Configs: map[string]config.Entry{
			"work": {File: "/tmp/.npmrc", Vault: "RnD", Item: "NPM token", Field: "password", Registry: "registry.npmjs.org"},
		},
	}
	var log strings.Builder
	d, saves := deps(t, &fakeSecret{token: "fresh"}, &fakeNpmrc{}, cfg, nil)
	d.Log = logger.New(&log)

	if err := refresh.Run(t.Context(), d); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(log.String(), "deprecated") {
		t.Errorf("log = %q, want a deprecation warning", log.String())
	}
	if *saves != 0 {
		t.Errorf("saved the config %d times, want 0", *saves)
	}
}
