// Package config persists which npmrc file pnop manages and where the token
// lives in 1Password.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/frodi-karlsson/pnop/internal/npmrc"
)

// DefaultFile is the npmrc pnop manages when an entry does not name one.
const DefaultFile = "~/.npmrc"

// Entry is one named credential configuration: which npmrc to keep in sync,
// and where its token lives in 1Password.
type Entry struct {
	// File is the npmrc pnop keeps in sync. pnpm reads ~/.npmrc.
	File string `toml:"file"`
	// Vault, Item and Field locate the token in 1Password.
	Vault string `toml:"vault"`
	Item  string `toml:"item"`
	Field string `toml:"field"`
	// Registry is the registry whose _authToken line is managed.
	Registry string `toml:"registry"`
}

// ErrNotConfigured is returned by Load when `pnop setup` has never been run.
var ErrNotConfigured = errors.New(
	"pnop is not configured yet - run: pnop setup -c <name> --vault=<vault> --item=<item> --field=<field>")

// Config is the whole on-disk document: a set of named entries plus a pointer
// to the one in force.
type Config struct {
	// Active names the entry every non-setup command uses.
	Active string `toml:"active"`
	// Configs holds every entry the user has defined, keyed by name.
	Configs map[string]Entry `toml:"configs"`
}

// Path returns the config file location, honouring XDG_CONFIG_HOME.
func Path() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "pnop", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".config", "pnop", "config.toml"), nil
}

// Load reads the config at path. A missing file yields ErrNotConfigured so the
// caller can print setup instructions rather than a bare stat error.
//
// Entry file paths are expanded here as well as on save: a hand-edited or
// dotfiles-synced config containing a literal "~" would otherwise be taken
// literally and write a credential into a directory named "~".
func Load(path string) (Config, error) {
	var cfg Config
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, ErrNotConfigured
		}
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}

	for name, entry := range cfg.Configs {
		entry = entry.WithDefaults()
		if err := entry.Validate(); err != nil {
			return cfg, fmt.Errorf("%s: config %q: %w", path, name, err)
		}
		file, err := ExpandPath(entry.File)
		if err != nil {
			return cfg, fmt.Errorf("%s: config %q: %w", path, name, err)
		}
		entry.File = file
		cfg.Configs[name] = entry
	}
	return cfg, nil
}

// Save writes cfg to path, creating parent directories as needed.
func Save(path string, cfg Config) error {
	for name, entry := range cfg.Configs {
		if err := entry.WithDefaults().Validate(); err != nil {
			return fmt.Errorf("config %q: %w", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	var sb strings.Builder
	if err := toml.NewEncoder(&sb).Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// WriteFile's mode only applies on creation; re-assert it so a config that
	// already existed with looser permissions is tightened.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// Validate reports whether the entry has everything needed to fetch a token.
func (e Entry) Validate() error {
	switch {
	case e.File == "":
		return errors.New("file is required")
	case e.Vault == "":
		return errors.New("vault is required")
	case e.Item == "":
		return errors.New("item is required")
	case e.Field == "":
		return errors.New("field is required")
	}
	return nil
}

// WithDefaults fills in the optional fields that callers may leave blank.
// Vault, Item and Field have no defaults: they describe the user's own
// 1Password layout, which pnop makes no assumptions about.
func (e Entry) WithDefaults() Entry {
	if e.File == "" {
		e.File = DefaultFile
	}
	if e.Registry == "" {
		e.Registry = npmrc.DefaultRegistry
	}
	// A registry pasted as a URL would otherwise build a malformed npmrc key
	// such as "//https://registry.npmjs.org//:_authToken=".
	e.Registry = strings.TrimSuffix(
		strings.TrimPrefix(strings.TrimPrefix(e.Registry, "https://"), "http://"), "/")
	return e
}

// ExpandPath resolves a leading ~ and makes the result absolute, so a config
// written on one machine still points somewhere sensible.
func ExpandPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locate home dir: %w", err)
		}
		p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", p, err)
	}
	return abs, nil
}
