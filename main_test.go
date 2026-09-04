package main

import (
	"bytes"
	"strings"
	"testing"
)

// `setup` is pnop's own; everything else - including `help` - must fall
// through to pnpm rather than being intercepted here.
func TestReservedWordsRouteToPnop(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"bare", nil},
		{"-h", []string{"-h"}},
		{"--help", []string{"--help"}},
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

func TestSetupIsRegisteredAsASubcommand(t *testing.T) {
	root := newRoot()

	cmd, _, err := root.Find([]string{"setup"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if cmd.Name() != "setup" {
		t.Errorf("resolved %q, want setup", cmd.Name())
	}
}

// `pnop help` and pnpm subcommands must NOT resolve to a pnop subcommand.
func TestPnpmCommandsAreNotIntercepted(t *testing.T) {
	root := newRoot()

	for _, arg := range []string{"help", "install", "up", "run", "publish"} {
		cmd, _, err := root.Find([]string{arg})
		if err != nil {
			t.Fatalf("Find(%q): %v", arg, err)
		}
		if cmd.Name() != root.Name() {
			t.Errorf("%q resolved to subcommand %q, want the root passthrough", arg, cmd.Name())
		}
	}
}
