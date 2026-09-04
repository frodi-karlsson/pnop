package npmrc_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/frodi-karlsson/pnop/internal/npmrc"
)

const registry = npmrc.DefaultRegistry

func TestReadTokenMissingFile(t *testing.T) {
	got, err := npmrc.FileStore{}.ReadToken(filepath.Join(t.TempDir(), "nope"), registry)
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if got != "" {
		t.Errorf("token = %q, want empty", got)
	}
}

func TestReadToken(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"single line", "//registry.npmjs.org/:_authToken=abc123\n", "abc123"},
		{"no trailing newline", "//registry.npmjs.org/:_authToken=abc123", "abc123"},
		{"among other entries", "engine-strict=true\n//registry.npmjs.org/:_authToken=abc123\nsave-exact=true\n", "abc123"},
		{"other registry only", "//npm.pkg.github.com/:_authToken=other\n", ""},
		{"empty file", "", ""},
		{"surrounding whitespace", "  //registry.npmjs.org/:_authToken=abc123  \n", "abc123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTemp(t, tt.content)
			got, err := npmrc.FileStore{}.ReadToken(path, registry)
			if err != nil {
				t.Fatalf("ReadToken: %v", err)
			}
			if got != tt.want {
				t.Errorf("token = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteTokenPreservesOtherLines(t *testing.T) {
	path := writeTemp(t, "engine-strict=true\n//registry.npmjs.org/:_authToken=old\n//npm.pkg.github.com/:_authToken=keepme\n")

	if err := (npmrc.FileStore{}).WriteToken(path, registry, "new"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	want := "engine-strict=true\n//registry.npmjs.org/:_authToken=new\n//npm.pkg.github.com/:_authToken=keepme\n"
	if got := readFile(t, path); got != want {
		t.Errorf("content =\n%q\nwant\n%q", got, want)
	}
}

func TestWriteTokenAppendsWhenAbsent(t *testing.T) {
	path := writeTemp(t, "engine-strict=true\n")

	if err := (npmrc.FileStore{}).WriteToken(path, registry, "new"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	want := "engine-strict=true\n//registry.npmjs.org/:_authToken=new\n"
	if got := readFile(t, path); got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestWriteTokenCreatesFileAndParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", ".npmrc")

	if err := (npmrc.FileStore{}).WriteToken(path, registry, "new"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	want := "//registry.npmjs.org/:_authToken=new\n"
	if got := readFile(t, path); got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestWriteTokenIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".npmrc")

	if err := (npmrc.FileStore{}).WriteToken(path, registry, "secret"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

func TestWriteTokenLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".npmrc")

	if err := (npmrc.FileStore{}).WriteToken(path, registry, "secret"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	// The lock file is expected to persist; removing it would race with any
	// other process waiting on it.
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file %s was left behind", e.Name())
		}
	}
}

// npm honours the last matching line, so pnop must read and rewrite that one.
// Acting on the first would either miss a stale token or "fix" an ineffective
// line and report success while the install keeps failing.
func TestLastDuplicateEntryWins(t *testing.T) {
	path := writeTemp(t, "//registry.npmjs.org/:_authToken=FIRST\n//registry.npmjs.org/:_authToken=LAST\n")

	got, err := npmrc.FileStore{}.ReadToken(path, registry)
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if got != "LAST" {
		t.Errorf("token = %q, want LAST (the entry npm actually uses)", got)
	}

	if err := (npmrc.FileStore{}).WriteToken(path, registry, "NEW"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	want := "//registry.npmjs.org/:_authToken=FIRST\n//registry.npmjs.org/:_authToken=NEW\n"
	if got := readFile(t, path); got != want {
		t.Errorf("content = %q, want the last entry rewritten: %q", got, want)
	}
}

// A dotfiles-managed npmrc is commonly a symlink; replacing the link with a
// regular file would silently detach it from the repo.
func TestWriteTokenFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles-npmrc")
	link := filepath.Join(dir, ".npmrc")

	if err := os.WriteFile(target, []byte("engine-strict=true\n"), 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := (npmrc.FileStore{}).WriteToken(link, registry, "tok"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink was replaced by a regular file")
	}
	want := "engine-strict=true\n//registry.npmjs.org/:_authToken=tok\n"
	if got := readFile(t, target); got != want {
		t.Errorf("target content = %q, want %q", got, want)
	}
}

// Two pnop runs in two terminals must not lose each other's unrelated entries.
func TestConcurrentWritesPreserveEveryEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".npmrc")
	if err := os.WriteFile(path, []byte("engine-strict=true\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const n = 8
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reg := fmt.Sprintf("r%d.example.com", i)
			if err := (npmrc.FileStore{}).WriteToken(path, reg, "tok"); err != nil {
				t.Errorf("WriteToken(%s): %v", reg, err)
			}
		}()
	}
	wg.Wait()

	content := readFile(t, path)
	for i := range n {
		entry := fmt.Sprintf("//r%d.example.com/:_authToken=tok", i)
		if !strings.Contains(content, entry) {
			t.Errorf("entry %q was lost; file is:\n%s", entry, content)
		}
	}
	if !strings.Contains(content, "engine-strict=true") {
		t.Error("the pre-existing unrelated line was lost")
	}
}

// The token in 1Password may have been pasted as a bare value or as the whole
// npmrc line; both must produce the same result.
func TestNormalizeToken(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare token", "npm_abc123", "npm_abc123"},
		{"full authToken line", "//registry.npmjs.org/:_authToken=npm_abc123", "npm_abc123"},
		{"full _auth line", "//registry.npmjs.org/:_auth=base64creds", "base64creds"},
		{"other registry line", "//npm.pkg.github.com/:_authToken=ghp_x", "ghp_x"},
		{"surrounding whitespace", "  npm_abc123\n", "npm_abc123"},
		{"line with whitespace", " //registry.npmjs.org/:_authToken= npm_abc \n", "npm_abc"},
		{"base64 padding is kept", "dG9rZW4=", "dG9rZW4="},
		{"bare token containing equals", "abc=def", "abc=def"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := npmrc.NormalizeToken(tt.in); got != tt.want {
				t.Errorf("NormalizeToken(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestWriteTokenNormalizesAFullLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".npmrc")

	if err := (npmrc.FileStore{}).WriteToken(path, registry, "//registry.npmjs.org/:_authToken=npm_abc"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	want := "//registry.npmjs.org/:_authToken=npm_abc\n"
	if got := readFile(t, path); got != want {
		t.Errorf("content = %q, want %q (not a doubled-up line)", got, want)
	}
}

func TestWriteThenReadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".npmrc")
	const token = "npm_AbC123-_xyz"

	if err := (npmrc.FileStore{}).WriteToken(path, registry, token); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}
	got, err := npmrc.FileStore{}.ReadToken(path, registry)
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if got != token {
		t.Errorf("token = %q, want %q", got, token)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".npmrc")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	return string(b)
}
