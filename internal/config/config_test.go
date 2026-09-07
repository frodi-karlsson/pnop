package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/pnop/internal/config"
)

func TestSaveIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.Config{
		Active:  "job",
		Configs: map[string]config.Entry{"job": {File: "/tmp/.npmrc", Command: "print tok"}},
	}

	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

func TestWithDefaultsNormalisesRegistryURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://registry.npmjs.org/", "registry.npmjs.org"},
		{"http://registry.npmjs.org", "registry.npmjs.org"},
		{"registry.npmjs.org", "registry.npmjs.org"},
	}

	for _, tt := range tests {
		got := config.Entry{File: "/tmp/.npmrc", Command: "print I", Registry: tt.in}.WithDefaults()
		if got.Registry != tt.want {
			t.Errorf("Registry(%q) = %q, want %q", tt.in, got.Registry, tt.want)
		}
	}
}

func TestWithDefaultsFillsFile(t *testing.T) {
	got := config.Entry{Command: "print I"}.WithDefaults()

	if got.File != "~/.npmrc" {
		t.Errorf("File = %q, want ~/.npmrc", got.File)
	}
}

func TestSaveTightensPermissionsOnAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := config.Config{
		Active:  "job",
		Configs: map[string]config.Entry{"job": {File: "/tmp/.npmrc", Command: "print I"}},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

func TestLoadMissingFileIsNotConfigured(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "absent.toml"))
	if !errors.Is(err, config.ErrNotConfigured) {
		t.Errorf("err = %v, want ErrNotConfigured", err)
	}
}

func TestLoadRejectsIncompleteConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	// Vault, item and field missing: a token could never be fetched from this.
	content := "active = \"job\"\n\n[configs.job]\nfile = \"/tmp/.npmrc\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := config.Load(path); err == nil {
		t.Error("Load succeeded, want validation error")
	}
}

func TestSaveRejectsIncompleteConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	cfg := config.Config{
		Active:  "job",
		Configs: map[string]config.Entry{"job": {File: "/tmp/.npmrc"}},
	}
	if err := config.Save(path, cfg); err == nil {
		t.Error("Save succeeded, want validation error")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("Save wrote a file despite validation failing")
	}
}

func TestWithDefaults(t *testing.T) {
	got := config.Entry{File: "/tmp/.npmrc", Command: "op read op://V/i/f"}.WithDefaults()

	if got.Registry != "registry.npmjs.org" {
		t.Errorf("Registry = %q, want registry.npmjs.org", got.Registry)
	}
}

// The command describes where a user keeps a token, so pnop must never guess.
func TestWithDefaultsNeverGuessesTheCommand(t *testing.T) {
	got := config.Entry{File: "/tmp/.npmrc"}.WithDefaults()

	if got.Command != "" {
		t.Errorf("WithDefaults invented command %q, want empty", got.Command)
	}
	if err := got.Validate(); err == nil {
		t.Error("Validate accepted a config with no command")
	}
}

// A config written before pnop took a command still works: the 1Password
// coordinates become the command they used to build, and stop being stored.
func TestWithDefaultsMigratesTheOldCoordinates(t *testing.T) {
	got := config.Entry{File: "/tmp/.npmrc", Vault: "MyVault", Item: "My Item", Field: "tokenfield"}.WithDefaults()

	want := config.LegacyCommand("MyVault", "My Item", "tokenfield")
	if got.Command != want {
		t.Errorf("Command = %q, want %q", got.Command, want)
	}
	if got.Vault != "" || got.Item != "" || got.Field != "" {
		t.Errorf("kept vault=%q item=%q field=%q, want them dropped", got.Vault, got.Item, got.Field)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("Validate: %v, want a migrated entry to be usable", err)
	}
}

// An explicit command wins over coordinates left behind in an old file.
func TestCommandBeatsTheOldCoordinates(t *testing.T) {
	got := config.Entry{File: "/tmp/.npmrc", Command: "mine", Vault: "V", Item: "I", Field: "F"}.WithDefaults()

	if got.Command != "mine" {
		t.Errorf("Command = %q, want the explicit one", got.Command)
	}
}

func TestWithDefaultsKeepsExplicitValues(t *testing.T) {
	got := config.Entry{
		File: "/tmp/.npmrc", Command: "print-token", Registry: "npm.pkg.github.com",
	}.WithDefaults()

	if got.Command != "print-token" {
		t.Errorf("Command = %q, want print-token", got.Command)
	}
	if got.Registry != "npm.pkg.github.com" {
		t.Errorf("Registry = %q, want npm.pkg.github.com", got.Registry)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"tilde slash", "~/.npmrc", filepath.Join(home, ".npmrc")},
		{"bare tilde", "~", home},
		{"absolute untouched", "/etc/npmrc", "/etc/npmrc"},
		{"tilde mid-path is literal", "/tmp/~/x", "/tmp/~/x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.ExpandPath(tt.in)
			if err != nil {
				t.Fatalf("ExpandPath: %v", err)
			}
			if got != tt.want {
				t.Errorf("ExpandPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExpandPathRejectsEmpty(t *testing.T) {
	if _, err := config.ExpandPath(""); err == nil {
		t.Error("ExpandPath(\"\") succeeded, want error")
	}
}

func TestPathHonoursXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/cfg")

	got, err := config.Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := "/custom/cfg/pnop/config.toml"; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestSaveLoadDocumentRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	want := config.Config{
		Active: "job",
		Configs: map[string]config.Entry{
			"job": {
				File: "/Users/someone/.npmrc", Vault: "MyVault",
				Item: "MyItem", Field: "MyField", Registry: "registry.npmjs.org",
			},
			"private": {
				File: "/Users/someone/.npmrc", Vault: "Employee",
				Item: ".npmrc.private", Field: "password", Registry: "registry.npmjs.org",
			},
		},
	}

	if err := config.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Active != "job" {
		t.Errorf("Active = %q, want job", got.Active)
	}
	if len(got.Configs) != 2 {
		t.Fatalf("got %d configs, want 2", len(got.Configs))
	}
	if got.Configs["private"].Command == "" {
		t.Error("private command was lost in the round trip")
	}
}

func TestLoadExpandsTildeInEveryEntry(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `active = "job"

[configs.job]
file = "~/.npmrc"
vault = "V"
item = "I"
field = "F"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if want := filepath.Join(home, ".npmrc"); cfg.Configs["job"].File != want {
		t.Errorf("File = %q, want %q", cfg.Configs["job"].File, want)
	}
}

func TestActiveEntry(t *testing.T) {
	cfg := config.Config{
		Active: "job",
		Configs: map[string]config.Entry{
			"job":     {File: "/tmp/.npmrc", Command: "print I"},
			"private": {File: "/tmp/.npmrc", Command: "print P"},
		},
	}

	got, err := cfg.ActiveEntry()
	if err != nil {
		t.Fatalf("ActiveEntry: %v", err)
	}
	if got.Command != "print I" {
		t.Errorf("Command = %q, want the active entry's", got.Command)
	}
}

func TestActiveEntryWithNoActiveSet(t *testing.T) {
	cfg := config.Config{
		Configs: map[string]config.Entry{"job": {File: "/tmp/.npmrc", Command: "print I"}},
	}

	if _, err := cfg.ActiveEntry(); !errors.Is(err, config.ErrNoActive) {
		t.Errorf("err = %v, want ErrNoActive", err)
	}
}

// A typo in `active`, or a hand-deleted entry, must say which names do exist.
func TestActiveEntryNamingAMissingConfig(t *testing.T) {
	cfg := config.Config{
		Active:  "jbo",
		Configs: map[string]config.Entry{"job": {}, "private": {}},
	}

	_, err := cfg.ActiveEntry()
	if err == nil {
		t.Fatal("ActiveEntry succeeded, want an error")
	}
	for _, want := range []string{"jbo", "job", "private"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q does not mention %q", err, want)
		}
	}
}
