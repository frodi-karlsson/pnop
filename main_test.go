package main

import (
	"bytes"
	"strings"
	"testing"
)

// Bare pnop has nothing to forward, so it introduces itself. `-h` and
// `--help` are pnpm's, and reach it.
func TestBareInvocationPrintsPnopHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"bare", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			root := newRoot()
			root.SetOut(&out)
			root.SetErr(&out)
			// cobra treats a nil SetArgs as "unset" and falls back to
			// os.Args[1:] (the test binary's own flags), so a bare
			// invocation must pass an empty, non-nil slice instead.
			args := tt.args
			if args == nil {
				args = []string{}
			}
			root.SetArgs(args)

			if err := root.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if !strings.Contains(out.String(), "pnop forwards every command to pnpm") {
				t.Errorf("output = %q, want pnop's own help", out.String())
			}
		})
	}
}

// Only the `+` forms are pnop's.
func TestSigilCommandsAreRegistered(t *testing.T) {
	root := newRoot()

	for _, name := range []string{"+setup", "+refresh", "+version", "+help"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("Find(%q): %v", name, err)
		}
		if cmd.Name() != name {
			t.Errorf("resolved %q, want %q", cmd.Name(), name)
		}
	}
}

// Everything without the sigil is pnpm's, `setup` and `refresh` included:
// pnpm has a `setup` of its own, and a repo may have a `refresh` script.
func TestPnpmCommandsAreNotIntercepted(t *testing.T) {
	root := newRoot()

	for _, arg := range []string{"help", "install", "up", "run", "publish", "setup", "refresh", "--version", "-v", "--help", "-h"} {
		cmd, _, err := root.Find([]string{arg})
		if err != nil {
			t.Fatalf("Find(%q): %v", arg, err)
		}
		if cmd.Name() != root.Name() {
			t.Errorf("%q resolved to subcommand %q, want the root passthrough", arg, cmd.Name())
		}
	}
}
