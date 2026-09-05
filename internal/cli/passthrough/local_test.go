package passthrough_test

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/pnop/internal/cli/passthrough"
	"github.com/frodi-karlsson/pnop/internal/logger"
)

// A failing test suite is the most common failure in a working day. It never
// contacted the registry, so it must not raise a 1Password prompt.
func TestLocalCommandFailuresSkipTheTokenCheck(t *testing.T) {
	local := [][]string{
		{"test"},
		{"t"},
		{"run", "build"},
		{"run", "test", "--", "--watch"},
		{"start"},
		{"exec", "tsc"},
		{"ls"},
		{"list", "--depth=1"},
		{"why", "lodash"},
		{"config", "get", "registry"},
		{"bin"},
		{"root"},
		{"init"},
		{"env", "use", "22"},
		{"rebuild"},
		{"prune"},
		{"pack"},
		{"link"},
		{"unlink"},
		{"-r", "test"},           // global flag before the subcommand
		{"--filter=web", "test"}, // flag with an inline value
	}

	for _, args := range local {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r := &fakeRunner{codes: []int{1}}
			s := &fakeSecret{token: freshToken}
			n := &fakeNpmrc{token: staleToken}
			var log strings.Builder

			err := passthrough.Run(t.Context(), deps(r, s, n, logger.New(&log)), args)

			assertExitCode(t, err, 1)
			if s.calls != 0 {
				t.Errorf("hit 1Password %d times, want 0 for a local-only command", s.calls)
			}
			if len(r.calls) != 1 {
				t.Errorf("ran pnpm %d times, want 1 (no rerun)", len(r.calls))
			}
			if len(n.writes) != 0 {
				t.Errorf("wrote the npmrc %d times, want 0", len(n.writes))
			}
			if log.String() != "" {
				t.Errorf("logged %q, want silence so the real failure stands alone", log.String())
			}
		})
	}
}

// Anything that can reach the registry must still recover, including
// subcommands this build has never heard of.
func TestRegistryCommandFailuresStillRecover(t *testing.T) {
	registry := [][]string{
		{"install"},
		{"i", "--frozen-lockfile"},
		{"add", "lodash"},
		{"up", "--latest"},
		{"update"},
		{"remove", "lodash"},
		{"fetch"},
		{"import"},
		{"dedupe"},
		{"outdated"},
		{"audit"},
		{"licenses", "list"},
		{"publish", "--dry-run"},
		{"dlx", "cowsay"},
		{"create", "vite"},
		{"patch", "lodash"},
		{"some-future-pnpm-command"}, // unknown defaults to recovering
		{"--filter", "web", "test"},  // value hides the subcommand; safe fallback
	}

	for _, args := range registry {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r := &fakeRunner{codes: []int{1, 0}}
			s := &fakeSecret{token: freshToken}
			n := &fakeNpmrc{token: staleToken}

			if err := passthrough.Run(t.Context(), deps(r, s, n, logger.Discard()), args); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if s.calls != 1 {
				t.Errorf("hit 1Password %d times, want 1", s.calls)
			}
			if len(r.calls) != 2 {
				t.Errorf("ran pnpm %d times, want 2 (rerun after refresh)", len(r.calls))
			}
		})
	}
}

// `pnpm add test` must not be mistaken for `pnpm test`.
func TestOnlyTheSubcommandPositionIsConsidered(t *testing.T) {
	r := &fakeRunner{codes: []int{1, 0}}
	s := &fakeSecret{token: freshToken}
	n := &fakeNpmrc{token: staleToken}

	if err := passthrough.Run(t.Context(), deps(r, s, n, logger.Discard()), []string{"add", "test"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if s.calls != 1 {
		t.Errorf("hit 1Password %d times, want 1: `add test` installs a package called test", s.calls)
	}
}
