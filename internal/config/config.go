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

// Config is the on-disk configuration written by `pnop setup`.
type Config struct {
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
var ErrNotConfigured = errors.New("pnop is not configured yet - run: pnop setup --file=~/.npmrc --vault=<vault> --item=<item> --field=<field>")

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
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	// Expand here as well as in setup: a config can arrive hand-edited or from
	// a dotfiles repo, and an unexpanded "~" would otherwise be taken
	// literally and write the token into a directory named "~".
	file, err := ExpandPath(cfg.File)
	if err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	cfg.File = file
	return cfg, nil
}

// Save writes cfg to path, creating parent directories as needed.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
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

// Validate reports whether the config has everything needed to fetch a token.
func (c Config) Validate() error {
	switch {
	case c.File == "":
		return errors.New("file is required")
	case c.Vault == "":
		return errors.New("vault is required")
	case c.Item == "":
		return errors.New("item is required")
	case c.Field == "":
		return errors.New("field is required")
	}
	return nil
}

// WithDefaults fills in the optional fields that callers may leave blank.
// Vault, Item and Field have no defaults: they describe the user's own
// 1Password layout, which pnop makes no assumptions about.
func (c Config) WithDefaults() Config {
	if c.Registry == "" {
		c.Registry = npmrc.DefaultRegistry
	}
	// A registry pasted as a URL would otherwise build a malformed npmrc key
	// such as "//https://registry.npmjs.org//:_authToken=".
	c.Registry = strings.TrimSuffix(
		strings.TrimPrefix(strings.TrimPrefix(c.Registry, "https://"), "http://"), "/")
	return c
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
