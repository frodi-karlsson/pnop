package passthrough_test

import (
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/pnop/internal/cli/passthrough"
	"github.com/frodi-karlsson/pnop/internal/negcache"
	"github.com/frodi-karlsson/pnop/internal/verify"
)

// Only a rejection is evidence about the token.
func TestOnlyRejectionReachesTheVault(t *testing.T) {
	tests := []struct {
		name      string
		outcome   verify.Outcome
		wantReads int
	}{
		{"registry accepts the token on disk", verify.Valid, 0},
		{"registry has no whoami, is down, or is unreachable", verify.Inconclusive, 0},
		{"registry refuses the token on disk", verify.Rejected, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness([]int{17}, staleToken, map[string]verify.Outcome{
				staleToken: tt.outcome,
				freshToken: verify.Valid,
			})

			err := h.run(t, "install")

			assertExitCode(t, err, 17)
			if h.secret.calls != tt.wantReads {
				t.Errorf("fetched from 1Password %d times, want %d", h.secret.calls, tt.wantReads)
			}
			if tt.wantReads == 0 && len(h.npmrc.writes) != 0 {
				t.Errorf("npmrc writes = %v, want none", h.npmrc.writes)
			}
		})
	}
}

// No token on disk means nothing to ask about, so the vault is read directly.
func TestMissingTokenSkipsTheProbe(t *testing.T) {
	for _, disk := range []string{"", "   "} {
		h := newHarness([]int{17}, disk, map[string]verify.Outcome{freshToken: verify.Valid})

		err := h.run(t, "install")

		assertExitCode(t, err, 17)
		for _, probed := range h.verifier.tokens {
			if probed == disk {
				t.Errorf("probed the registry with an empty token %q", disk)
			}
		}
		if h.secret.calls != 1 {
			t.Errorf("fetched from 1Password %d times, want 1", h.secret.calls)
		}
		if len(h.npmrc.writes) != 1 {
			t.Errorf("npmrc writes = %v, want the vault's token written", h.npmrc.writes)
		}
	}
}

func TestRejectedVaultTokenIsNotWritten(t *testing.T) {
	tests := []struct {
		name  string
		vault string
		want  string
	}{
		{"same token in both places", staleToken, "same rejected token"},
		{"vault has a newer token, also dead", "newer-but-dead", "newer token and it is also rejected"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness([]int{17}, staleToken, map[string]verify.Outcome{
				staleToken: verify.Rejected,
				tt.vault:   verify.Rejected,
			})
			h.secret.token = tt.vault

			err := h.run(t, "install")

			assertExitCode(t, err, 17)
			if len(h.npmrc.writes) != 0 {
				t.Errorf("npmrc writes = %v, want none: the vault's token is dead too", h.npmrc.writes)
			}
			if len(h.cache.recorded) != 1 || h.cache.recorded[0] != negcache.Rejected {
				t.Errorf("cache records = %v, want one %q", h.cache.recorded, negcache.Rejected)
			}
			if !strings.Contains(h.log.String(), tt.want) {
				t.Errorf("log = %q, want it to contain %q", h.log.String(), tt.want)
			}
		})
	}
}

// The one place the old fresh-vs-current comparison still decides anything.
func TestUncheckableVaultTokenFallsBackToComparison(t *testing.T) {
	t.Run("identical to disk", func(t *testing.T) {
		h := newHarness([]int{17}, staleToken, map[string]verify.Outcome{
			staleToken: verify.Rejected, // the disk probe answered; the vault probe did not
		})
		h.secret.token = staleToken
		// Same token, different answer the second time: drive it by call order.
		h.verifier.byCall = []verify.Outcome{verify.Rejected, verify.Inconclusive}

		err := h.run(t, "install")

		assertExitCode(t, err, 17)
		if len(h.npmrc.writes) != 0 {
			t.Errorf("npmrc writes = %v, want none: nothing would change", h.npmrc.writes)
		}
		if len(h.cache.recorded) != 1 || h.cache.recorded[0] != negcache.Unchecked {
			t.Errorf("cache records = %v, want one %q", h.cache.recorded, negcache.Unchecked)
		}
	})

	t.Run("different from disk", func(t *testing.T) {
		h := newHarness([]int{17}, staleToken, nil)
		h.verifier.byCall = []verify.Outcome{verify.Rejected, verify.Inconclusive}

		err := h.run(t, "install")

		assertExitCode(t, err, 17)
		if len(h.npmrc.writes) != 1 || h.npmrc.writes[0] != freshToken {
			t.Errorf("npmrc writes = %v, want [%s]: a different token cannot be worse than a rejected one",
				h.npmrc.writes, freshToken)
		}
		if len(h.cache.recorded) != 0 {
			t.Errorf("cache records = %v, want none: something was written", h.cache.recorded)
		}
	})
}

// The cached message must not read as a diagnosis of what failed on screen.
func TestCacheHitSkipsTheVault(t *testing.T) {
	h := newHarness([]int{17}, staleToken, rejected())
	h.cache.hit = true
	h.cache.entry = negcache.Entry{Reason: negcache.Rejected, Age: 3 * time.Minute}

	err := h.run(t, "test")

	assertExitCode(t, err, 17)
	if h.secret.calls != 0 {
		t.Errorf("fetched from 1Password %d times, want 0 on a cache hit", h.secret.calls)
	}
	log := h.log.String()
	if !strings.Contains(log, "pnpm failed above") {
		t.Errorf("log = %q, want it to name pnpm's failure as the failure on screen", log)
	}
	if !strings.Contains(log, "rejected 3 minutes ago") {
		t.Errorf("log = %q, want the stored reason and age", log)
	}
}

// An empty npmrc under one profile must not silence another.
func TestCacheIsKeyedByConfigName(t *testing.T) {
	h := newHarness([]int{17}, "", map[string]verify.Outcome{freshToken: verify.Rejected})

	_ = h.run(t, "install")

	if len(h.cache.keys) == 0 {
		t.Fatal("cache was never consulted")
	}
	for _, key := range h.cache.keys {
		if !strings.HasPrefix(key, configName+"/") {
			t.Errorf("cache key = %q, want it scoped to the config name", key)
		}
	}
}

// The marker goes only where a wrapper asked for it.
func TestRefreshMarkerOnlyWhenRequested(t *testing.T) {
	valid := map[string]verify.Outcome{staleToken: verify.Rejected, freshToken: verify.Valid}

	t.Run("no variable set", func(t *testing.T) {
		h := newHarness([]int{17}, staleToken, valid)

		_ = h.run(t, "install")

		if len(h.markers) != 0 {
			t.Errorf("wrote markers %v, want none in a default install", h.markers)
		}
	})

	t.Run("wrapper supplies the path", func(t *testing.T) {
		h := newHarness([]int{17}, staleToken, valid)
		h.env[passthrough.MarkerEnv] = "/tmp/pnop.4321.marker"

		_ = h.run(t, "install")

		if len(h.markers) != 1 || h.markers[0] != "/tmp/pnop.4321.marker" {
			t.Errorf("markers = %v, want the wrapper's path once", h.markers)
		}
	})
}

// The rerun is opt-in, and its marker goes on the child's environment only.
func TestOptInRerunMarksOnlyTheChild(t *testing.T) {
	h := newHarness([]int{17, 0}, staleToken, map[string]verify.Outcome{
		staleToken: verify.Rejected,
		freshToken: verify.Valid,
	})
	h.entry.Rerun = true

	if err := h.run(t, "install"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(h.runner.calls) != 2 {
		t.Fatalf("ran pnpm %d times, want 2", len(h.runner.calls))
	}
	if h.runner.envs[0] != nil {
		t.Errorf("first run got extra env %v, want none", h.runner.envs[0])
	}
	if !contains(h.runner.envs[1], passthrough.RetriedEnv+"=1") {
		t.Errorf("rerun env = %v, want it to carry %s=1", h.runner.envs[1], passthrough.RetriedEnv)
	}
}

func TestRerunFailureReportsSecondExitCode(t *testing.T) {
	h := newHarness([]int{17, 9}, staleToken, map[string]verify.Outcome{
		staleToken: verify.Rejected,
		freshToken: verify.Valid,
	})
	h.env[passthrough.RerunEnv] = "1"

	err := h.run(t, "install")

	assertExitCode(t, err, 9)
}

// One refresh is the whole budget, reruns and nested scripts included.
func TestRetriedEnvSkipsRecovery(t *testing.T) {
	h := newHarness([]int{17}, staleToken, rejected())
	h.env[passthrough.RetriedEnv] = "1"

	err := h.run(t, "install")

	assertExitCode(t, err, 17)
	if h.verifier.calls() != 0 {
		t.Errorf("probed %d times, want 0 inside a rerun", h.verifier.calls())
	}
	if h.secret.calls != 0 {
		t.Errorf("fetched from 1Password %d times, want 0 inside a rerun", h.secret.calls)
	}
}

// The printed command has to survive a paste back into a shell.
func TestReprintedCommandIsShellSafe(t *testing.T) {
	h := newHarness([]int{17}, staleToken, map[string]verify.Outcome{
		staleToken: verify.Rejected,
		freshToken: verify.Valid,
	})

	_ = h.run(t, "--filter", "!web", "add", "foo bar")

	want := `run it again: pnpm --filter '!web' add 'foo bar'`
	if !strings.Contains(h.log.String(), want) {
		t.Errorf("log = %q, want it to contain %q", h.log.String(), want)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// A PATH shim named `pnpm` would otherwise resolve back to itself.
func TestBinOverrideIsUsedForBothRuns(t *testing.T) {
	h := newHarness([]int{17, 0}, staleToken, map[string]verify.Outcome{
		staleToken: verify.Rejected,
		freshToken: verify.Valid,
	})
	h.env[passthrough.BinEnv] = "/opt/homebrew/bin/pnpm"
	h.entry.Rerun = true

	if err := h.run(t, "install"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for i, call := range h.runner.calls {
		if call[0] != "/opt/homebrew/bin/pnpm" {
			t.Errorf("run %d invoked %q, want the override", i, call[0])
		}
	}
}

// One registry and one vault field per config, so a scoped specifier in argv
// must not redirect the probe.
func TestProbeTargetIsAlwaysTheConfiguredRegistry(t *testing.T) {
	for _, args := range [][]string{
		{"install"},
		{"add", "@scope/pkg"},
		{"dlx", "@scope/tool"},
	} {
		h := newHarness([]int{17}, staleToken, rejected())

		_ = h.run(t, args...)

		if len(h.verifier.registries) == 0 {
			t.Fatalf("%v: never probed", args)
		}
		for _, got := range h.verifier.registries {
			if got != h.entry.Registry {
				t.Errorf("%v: probed %q, want the configured %q", args, got, h.entry.Registry)
			}
		}
	}
}
