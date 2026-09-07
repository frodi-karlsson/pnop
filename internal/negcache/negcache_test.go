package negcache_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/pnop/internal/negcache"
)

func TestRecordThenLookup(t *testing.T) {
	dir := negcache.Dir(t.TempDir())

	if err := dir.Record("work", "tok", negcache.Rejected); err != nil {
		t.Fatalf("Record: %v", err)
	}

	entry, ok := dir.Lookup("work", "tok")
	if !ok {
		t.Fatal("Lookup missed an entry just recorded")
	}
	if entry.Reason != negcache.Rejected {
		t.Errorf("reason = %q, want %q", entry.Reason, negcache.Rejected)
	}
	if entry.Age > time.Minute {
		t.Errorf("age = %v, want it to be fresh", entry.Age)
	}
}

// A rewritten token is a different key, which is what invalidates an entry.
func TestADifferentTokenMisses(t *testing.T) {
	dir := negcache.Dir(t.TempDir())
	if err := dir.Record("work", "old", negcache.Rejected); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if _, ok := dir.Lookup("work", "new"); ok {
		t.Error("a rewritten token still hit the cache")
	}
}

// With no token there is one fixed hash input, so the config name separates it.
func TestEmptyTokensDoNotCollideAcrossConfigs(t *testing.T) {
	dir := negcache.Dir(t.TempDir())
	if err := dir.Record("work", "", negcache.Rejected); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if _, ok := dir.Lookup("personal", ""); ok {
		t.Error("an entry for one config suppressed another with an equally empty npmrc")
	}
	if _, ok := dir.Lookup("work", ""); !ok {
		t.Error("the config that recorded the entry did not find it")
	}
}

func TestExpiredEntryMissesAndIsRemoved(t *testing.T) {
	dir := t.TempDir()
	cache := negcache.Dir(dir)
	if err := cache.Record("work", "tok", negcache.Rejected); err != nil {
		t.Fatalf("Record: %v", err)
	}

	// Age it by the stored timestamp, not the mtime.
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("ReadDir = %v, %v, want one entry", files, err)
	}
	path := filepath.Join(dir, files[0].Name())
	old := strconv.FormatInt(time.Now().Add(-negcache.TTL-time.Minute).Unix(), 10)
	if err := os.WriteFile(path, []byte(old+" rejected\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, ok := cache.Lookup("work", "tok"); ok {
		t.Error("an expired entry still suppressed a vault read")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("an expired entry was left behind to accumulate")
	}
}

func TestMalformedEntryMisses(t *testing.T) {
	dir := t.TempDir()
	cache := negcache.Dir(dir)
	if err := cache.Record("work", "tok", negcache.Unchecked); err != nil {
		t.Fatalf("Record: %v", err)
	}
	files, _ := os.ReadDir(dir)
	path := filepath.Join(dir, files[0].Name())
	if err := os.WriteFile(path, []byte("garbage\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, ok := cache.Lookup("work", "tok"); ok {
		t.Error("a malformed entry was treated as a hit")
	}
}

// Nothing secret lands here, but it is still owner-only.
func TestEntriesAreOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	cache := negcache.Dir(dir)
	if err := cache.Record("work", "tok", negcache.Rejected); err != nil {
		t.Fatalf("Record: %v", err)
	}

	files, _ := os.ReadDir(dir)
	info, err := os.Stat(filepath.Join(dir, files[0].Name()))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600", perm)
	}
	// The token must not be recoverable from the file name either.
	if strings.Contains(files[0].Name(), "tok") {
		t.Errorf("file name %q carries the token", files[0].Name())
	}
}

// The message has to distinguish a rejection from an unanswered question.
func TestDescribeNamesTheReason(t *testing.T) {
	tests := []struct {
		entry negcache.Entry
		want  string
	}{
		{negcache.Entry{Reason: negcache.Rejected, Age: 3 * time.Minute}, "rejected 3 minutes ago"},
		{negcache.Entry{Reason: negcache.Rejected, Age: 61 * time.Second}, "rejected 1 minute ago"},
		{negcache.Entry{Reason: negcache.Unchecked, Age: 2 * time.Minute}, "could not be checked 2 minutes ago"},
	}

	for _, tt := range tests {
		if got := tt.entry.Describe(); !strings.Contains(got, tt.want) {
			t.Errorf("Describe() = %q, want it to contain %q", got, tt.want)
		}
	}
}
