// Package negcache bounds token-command runs that have already proved fruitless.
package negcache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// TTL is how long an entry suppresses another run of the token command.
const TTL = 10 * time.Minute

const fileMode fs.FileMode = 0o600

// Reason is why running the token command did not help.
type Reason string

const (
	Rejected  Reason = "rejected"
	Unchecked Reason = "unchecked"
)

// Entry is a recorded fruitless read.
type Entry struct {
	Reason Reason
	Age    time.Duration
}

// Describe renders the entry for the user.
func (e Entry) Describe() string {
	mins := int(e.Age.Round(time.Minute) / time.Minute)
	unit := "minutes"
	if mins == 1 {
		unit = "minute"
	}
	if e.Reason == Rejected {
		return fmt.Sprintf("the vault's token was rejected %d %s ago", mins, unit)
	}
	return fmt.Sprintf("the vault's token could not be checked %d %s ago", mins, unit)
}

// Cache records and looks up token-command runs that did not help.
type Cache interface {
	Lookup(config, token string) (Entry, bool)
	Record(config, token string, reason Reason) error
}

// Dir is a filesystem-backed Cache: one small file per key.
type Dir string

// Default returns the per-user directory pnop uses. Not /tmp, which is a shared
// namespace where a fixed filename may already exist owned by someone else.
func Default() (Dir, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate cache dir: %w", err)
	}
	return Dir(filepath.Join(base, "pnop", "probe")), nil
}

// Lookup implements Cache, removing an expired entry on the way out.
func (d Dir) Lookup(config, token string) (Entry, bool) {
	path := d.path(config, token)
	b, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, false
	}

	reason, at, err := parse(string(b))
	if err != nil {
		_ = os.Remove(path)
		return Entry{}, false
	}

	age := time.Since(at)
	if age < 0 || age > TTL {
		_ = os.Remove(path)
		return Entry{}, false
	}
	return Entry{Reason: reason, Age: age}, true
}

// Record implements Cache.
func (d Dir) Record(config, token string, reason Reason) error {
	if err := os.MkdirAll(string(d), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", string(d), err)
	}
	line := strconv.FormatInt(time.Now().Unix(), 10) + " " + string(reason) + "\n"
	path := d.path(config, token)
	if err := os.WriteFile(path, []byte(line), fileMode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// WriteFile's mode only applies on creation.
	if err := os.Chmod(path, fileMode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

func (d Dir) path(config, token string) string {
	return filepath.Join(string(d), key(config, token))
}

// key hashes the config name with the token. The name matters because two
// configs with an equally empty npmrc would otherwise share one key, and a
// rewrite of the token is what invalidates an entry everywhere else.
func key(config, token string) string {
	sum := sha256.Sum256([]byte(config + "\x00" + token))
	return hex.EncodeToString(sum[:])[:16]
}

// parse reads "<unix seconds> <reason>". The timestamp is stored rather than
// taken from the mtime, so copying the directory cannot extend a TTL.
func parse(content string) (Reason, time.Time, error) {
	secs, rest, found := strings.Cut(strings.TrimSpace(content), " ")
	if !found {
		return "", time.Time{}, errors.New("malformed cache entry")
	}
	n, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("malformed timestamp: %w", err)
	}
	reason := Reason(strings.TrimSpace(rest))
	if reason != Rejected && reason != Unchecked {
		return "", time.Time{}, fmt.Errorf("unknown reason %q", reason)
	}
	return reason, time.Unix(n, 0), nil
}
