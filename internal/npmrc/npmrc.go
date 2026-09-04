// Package npmrc reads and writes registry auth tokens in .npmrc-style files.
package npmrc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// DefaultRegistry is the registry whose auth token pnop manages by default.
const DefaultRegistry = "registry.npmjs.org"

// fileMode is deliberately owner-only: these files hold credentials.
const fileMode fs.FileMode = 0o600

// tokenMarkers are the npmrc keys that carry a credential, longest first so
// that ":_authToken=" is preferred over the ":_auth=" substring.
var tokenMarkers = []string{":_authToken=", ":_auth="}

// Store reads and writes auth tokens for a registry.
type Store interface {
	ReadToken(path, registry string) (string, error)
	WriteToken(path, registry, token string) error
}

// FileStore is the real filesystem-backed Store.
type FileStore struct{}

// ReadToken returns the auth token for registry in the npmrc at path.
// A missing file or a missing entry yields an empty token and no error, so
// callers can treat "no token yet" the same as "a different token".
//
// When a file contains the same key more than once, the *last* occurrence is
// returned: that is the one npm and pnpm actually use.
func (FileStore) ReadToken(path, registry string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	prefix := tokenKey(registry)
	token := ""
	for line := range strings.SplitSeq(string(b), "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			token = strings.TrimSpace(after)
		}
	}
	return token, nil
}

// WriteToken sets the auth token for registry in the npmrc at path, leaving
// every other line intact.
//
// The read-modify-write is guarded by an advisory lock on a sibling lock file
// so that two concurrent pnop runs cannot lose each other's unrelated lines,
// and the new content is fsynced and renamed into place so a crash can never
// leave a truncated or empty npmrc.
func (FileStore) WriteToken(path, registry, token string) error {
	// Follow symlinks so a dotfiles-managed npmrc is updated through the link
	// rather than being replaced by a regular file.
	path, err := resolveTarget(path)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}

	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	next := upsert(string(existing), tokenKey(registry), NormalizeToken(token))

	tmp, err := os.CreateTemp(dir, ".npmrc-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName) // no-op once the rename below succeeds
	}()

	if err := writeSyncClose(tmp, next); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, fileMode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// NormalizeToken accepts either a bare token or a whole npmrc line and returns
// the bare token, so it does not matter which of the two a user pasted into
// 1Password. Only a value that actually looks like an npmrc entry is trimmed;
// a bare token is returned untouched, including any trailing "=" padding.
func NormalizeToken(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "//") {
		return value
	}
	for _, marker := range tokenMarkers {
		if _, after, found := strings.Cut(value, marker); found {
			return strings.TrimSpace(after)
		}
	}
	return value
}

// resolveTarget resolves symlinks in path. A path that does not exist yet is
// returned as-is, since there is nothing to follow.
func resolveTarget(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return path, nil
		}
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	return resolved, nil
}

// lock takes an exclusive advisory lock for path, returning the release func.
func lock(path string) (func(), error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, fmt.Errorf("open lock file for %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func writeSyncClose(f *os.File, content string) error {
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	// Flush to disk before the rename, so the rename cannot land ahead of the
	// data and leave an empty file behind after a power loss.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	return nil
}

// upsert replaces the value of the last line starting with prefix - the one
// npm honours - or appends the entry when the file has no such line yet.
func upsert(content, prefix, token string) string {
	entry := prefix + token

	lines := strings.Split(content, "\n")
	last := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			last = i
		}
	}
	if last >= 0 {
		lines[last] = entry
		return strings.Join(lines, "\n")
	}

	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return entry + "\n"
	}
	return trimmed + "\n" + entry + "\n"
}

func tokenKey(registry string) string {
	return "//" + registry + "/:_authToken="
}
