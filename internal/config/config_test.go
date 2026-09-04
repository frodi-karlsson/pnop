package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frodi-karlsson/pni/internal/config"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	want := config.Config{
		File:     "/Users/someone/.npmrc",
		Vault:    "MyVault",
		Item:     "MyItem",
		Field:    "MyField",
		Registry: "registry.npmjs.org",
	}

	if err := config.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Errorf("config = %+v, want %+v", got, want)
	}
}

func TestSaveIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.Config{File: "/tmp/.npmrc", Vault: "MyVault", Item: "tok", Field: "tokenfield"}

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

// A config can arrive hand-edited or from a dotfiles repo. An unexpanded "~"
// would be taken literally and write the npm token into a directory named "~"
// under the current working directory.
func TestLoadExpandsTildeInFile(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	content := "file = \"~/.npmrc\"\nvault = \"V\"\nitem = \"I\"\nfield = \"F\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if want := filepath.Join(home, ".npmrc"); cfg.File != want {
		t.Errorf("File = %q, want %q", cfg.File, want)
	}
}

func TestWithDefaultsNormalisesRegistryURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://registry.npmjs.org/", "registry.npmjs.org"},
		{"http://registry.npmjs.org", "registry.npmjs.org"},
		{"registry.npmjs.org", "registry.npmjs.org"},
	}

	for _, tt := range tests {
		got := config.Config{File: "/tmp/.npmrc", Vault: "V", Item: "I", Field: "F", Registry: tt.in}.WithDefaults()
		if got.Registry != tt.want {
			t.Errorf("Registry(%q) = %q, want %q", tt.in, got.Registry, tt.want)
		}
	}
}

func TestSaveTightensPermissionsOnAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := config.Save(path, config.Config{File: "/tmp/.npmrc", Vault: "V", Item: "I", Field: "F"}); err != nil {
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
	// Vault and item missing: a token could never be fetched from this.
	if err := os.WriteFile(path, []byte("file = \"/tmp/.npmrc\"\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := config.Load(path); err == nil {
		t.Error("Load succeeded, want validation error")
	}
}

func TestSaveRejectsIncompleteConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	if err := config.Save(path, config.Config{File: "/tmp/.npmrc"}); err == nil {
		t.Error("Save succeeded, want validation error")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("Save wrote a file despite validation failing")
	}
}

func TestWithDefaults(t *testing.T) {
	got := config.Config{File: "/tmp/.npmrc", Vault: "MyVault", Item: "tok", Field: "tokenfield"}.WithDefaults()

	if got.Registry != "registry.npmjs.org" {
		t.Errorf("Registry = %q, want registry.npmjs.org", got.Registry)
	}
}

// Vault, item and field describe the user's own 1Password layout, so pni must
// never guess them.
func TestWithDefaultsNeverGuessesTheItemLayout(t *testing.T) {
	got := config.Config{File: "/tmp/.npmrc"}.WithDefaults()

	if got.Vault != "" || got.Item != "" || got.Field != "" {
		t.Errorf("WithDefaults invented vault=%q item=%q field=%q, want all empty",
			got.Vault, got.Item, got.Field)
	}
	if err := got.Validate(); err == nil {
		t.Error("Validate accepted a config with no vault/item/field")
	}
}

func TestWithDefaultsKeepsExplicitValues(t *testing.T) {
	got := config.Config{
		File: "/tmp/.npmrc", Vault: "MyVault", Item: "tok",
		Field: "otherfield", Registry: "npm.pkg.github.com",
	}.WithDefaults()

	if got.Field != "otherfield" {
		t.Errorf("Field = %q, want otherfield", got.Field)
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
	if want := "/custom/cfg/pni/config.toml"; got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}
