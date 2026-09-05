// Package authfail recognises a registry authentication failure in pnpm's
// output.
//
// This replaces an earlier attempt to guess from the command line whether a
// failure could have been credential related. That could not work: pnpm runs
// any package.json script as a bare subcommand, so `pnpm typecheck` and
// `pnpm upd` are indistinguishable from each other and from an unknown pnpm
// command, yet one runs a compiler and the other runs `pnpm update`. Whether
// the registry was contacted is a fact about what the process did, not about
// what its arguments looked like.
package authfail

import "strings"

// signatures are matched case-insensitively against what pnpm printed.
//
// The first is the strongest: pnpm emits it whenever it sent a credential,
// which is exactly the condition that makes a refresh worth trying. The fetch
// codes cover 404 as well as 401 and 403, because npm answers 404 rather than
// 401 for a private package the caller may not read, and that 404 is the shape
// a genuinely stale token takes in practice.
// The wording changes between pnpm majors, so both shapes are listed. pnpm 11
// prints a single bracketed line; pnpm 12 prints a boxed multi-line error and
// uses a different code for a tarball fetch, where it also omits the
// authorization line entirely. Any new pnpm major deserves a check here.
var signatures = []string{
	"an authorization header was used",
	"err_pnpm_fetch_401",
	"err_pnpm_fetch_403",
	"err_pnpm_fetch_404",
	// pnpm 12 tarball failures. The code is matched rather than the wrapped
	// prose beneath it, because the box wraps to terminal width and can split
	// the status text across lines. A 5xx here costs one needless 1Password
	// read, which the token comparison then discards.
	"err_pnpm_tarball_http_status",
	"eneedauth",
	"401 unauthorized",
	"403 forbidden",
}

// Detected reports whether output looks like a registry authentication
// failure.
//
// A false positive costs one needless 1Password read, since the token
// comparison still gates the rewrite and the rerun. A false negative costs an
// unrecovered failure, so the patterns lean inclusive.
func Detected(output string) bool {
	if output == "" {
		return false
	}
	lower := strings.ToLower(output)
	for _, sig := range signatures {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}
