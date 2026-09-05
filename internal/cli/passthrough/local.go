package passthrough

import "strings"

// localCommands never contact the registry, so a failure from one of them can
// never be caused by a stale npm token. Recovery is skipped for these, which
// is what keeps a failing `pnpm test` from raising a 1Password prompt.
//
// This is deliberately a list of commands to SKIP rather than a list of
// commands to cover. An unrecognised command is treated as registry touching,
// so a subcommand added by a future pnpm release is handled automatically.
// The list drifting costs at most a needless prompt, never a missed refresh.
var localCommands = map[string]bool{
	"approve-builds": true,
	"bin":            true,
	"c":              true,
	"cat-file":       true,
	"cat-index":      true,
	"config":         true,
	"env":            true,
	"exec":           true,
	"find-hash":      true,
	"ignored-builds": true,
	"init":           true,
	"link":           true,
	"ln":             true,
	"list":           true,
	"ls":             true,
	"pack":           true,
	"patch-commit":   true,
	"patch-remove":   true,
	"prune":          true,
	"rb":             true,
	"rebuild":        true,
	"root":           true,
	"run":            true,
	"run-script":     true,
	"start":          true,
	"t":              true,
	"test":           true,
	"unlink":         true,
	"why":            true,
}

// touchesRegistry reports whether a failed invocation could plausibly have
// failed because of credentials, and so is worth checking the token for.
func touchesRegistry(args []string) bool {
	return !localCommands[subcommand(args)]
}

// subcommand returns the first non-flag token, which is pnpm's subcommand.
//
// A flag taking a space separated value can hide the subcommand behind that
// value, as in `pnpm --filter web test`, where this returns "web". That
// matches nothing and recovery is attempted, which is the safe direction: a
// needless prompt rather than a missed refresh. The `--filter=web` form has
// no such problem.
func subcommand(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}
