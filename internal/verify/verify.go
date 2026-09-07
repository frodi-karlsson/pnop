// Package verify asks a registry whether a token is still accepted, which is
// the gate pnop uses in place of reading pnpm's error prose.
package verify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/frodi-karlsson/pnop/internal/logger"
)

// Outcome is what a registry said about a token. There are three and not four:
// an answer that is neither 200 nor 401 describes the endpoint, not the
// credential, and belongs with an unreachable host.
type Outcome int

const (
	Inconclusive Outcome = iota
	Valid
	Rejected
)

// String implements fmt.Stringer.
func (o Outcome) String() string {
	switch o {
	case Valid:
		return "valid"
	case Rejected:
		return "rejected"
	default:
		return "inconclusive"
	}
}

// Timeout caps a probe. pnop is already on the failure path of a command
// someone is waiting on, which is also why there are no retries.
const Timeout = 2 * time.Second

const maxBody = 4 << 10

// Verifier reports whether a registry still accepts a token. It returns no
// error: Inconclusive already absorbs every transport failure, and a second
// channel would invite callers to rebuild the distinction.
type Verifier interface {
	Verify(ctx context.Context, registry, token string) Outcome
}

// Identifier also reports the username, which `pnop +setup` shows.
type Identifier interface {
	Verifier
	Identify(ctx context.Context, registry, token string) (string, Outcome)
}

// HTTP is the real Verifier.
type HTTP struct {
	Client *http.Client
	Log    logger.Logger
}

// Verify implements Verifier.
func (h HTTP) Verify(ctx context.Context, registry, token string) Outcome {
	_, outcome := h.Identify(ctx, registry, token)
	return outcome
}

// Identify probes registry with token. The username is empty unless the
// registry accepted it and answered in npm's shape.
func (h HTTP) Identify(ctx context.Context, registry, token string) (string, Outcome) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	url := "https://" + strings.Trim(registry, "/") + "/-/whoami"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		h.logf("could not build a whoami request for %s: %v", registry, err)
		return "", Inconclusive
	}
	// Unauthenticated would answer 401 and report a credential that does not
	// exist as rejected, so send nothing at all.
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := h.client().Do(req)
	if err != nil {
		h.logf("could not reach %s: %v", registry, err)
		return "", Inconclusive
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// The status is the answer; an unreadable body costs only the name.
		return username(resp.Body), Valid
	case http.StatusUnauthorized:
		return "", Rejected
	default:
		h.logf("%s answered %d to whoami, which says nothing about the token",
			registry, resp.StatusCode)
		return "", Inconclusive
	}
}

func (h HTTP) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return http.DefaultClient
}

func (h HTTP) logf(format string, args ...any) {
	if h.Log == nil {
		return
	}
	h.Log.Infof(format, args...)
}

func username(r io.Reader) string {
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(io.LimitReader(r, maxBody)).Decode(&body); err != nil {
		return ""
	}
	return body.Username
}
