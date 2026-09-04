// Package secret fetches the npm token out of 1Password.
package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Fetcher retrieves a single secret field.
type Fetcher interface {
	Fetch(ctx context.Context, vault, item, field string) (string, error)
}

// OP shells out to the 1Password CLI, reusing whatever session the user
// already has (including Touch ID).
type OP struct {
	// Bin is the op executable; empty means "op" on PATH.
	Bin string
	// Stdin and Stderr are wired to the terminal so op can prompt for
	// biometric unlock. Only stdout is captured, since that carries the secret.
	Stdin  io.Reader
	Stderr io.Writer
}

// Fetch returns the value of field on item in vault.
//
// `op item get` is used rather than an op:// secret reference because item
// titles routinely contain characters (parentheses, spaces) that are illegal
// in a secret reference.
func (o OP) Fetch(ctx context.Context, vault, item, field string) (string, error) {
	bin := o.Bin
	if bin == "" {
		bin = "op"
	}

	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, bin,
		"item", "get", item,
		"--vault", vault,
		"--fields", "label="+field,
		"--reveal",
	)
	cmd.Stdin = o.Stdin
	cmd.Stdout = &stdout
	cmd.Stderr = o.Stderr

	if err := cmd.Run(); err != nil {
		var notFound *exec.Error
		if errors.As(err, &notFound) {
			return "", fmt.Errorf("1Password CLI not found (%s): %w", bin, err)
		}
		return "", fmt.Errorf("op item get %q in vault %q: %w", item, vault, err)
	}

	token := strings.TrimSpace(stdout.String())
	if token == "" {
		return "", fmt.Errorf("1Password returned an empty %q field for item %q", field, item)
	}
	return token, nil
}
