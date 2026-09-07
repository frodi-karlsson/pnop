package verify_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frodi-karlsson/pnop/internal/verify"
)

// server returns a registry stand-in and the host pnop would have stored.
func server(t *testing.T, handler http.HandlerFunc) (verify.HTTP, string) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	return verify.HTTP{Client: srv.Client()}, strings.TrimPrefix(srv.URL, "https://")
}

// Only 401 is evidence against a token. GitHub Packages answers 404 to whoami
// and an outage answers 5xx, neither of which is about the credential.
func TestStatusDecidesTheOutcome(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   verify.Outcome
	}{
		{"accepted", http.StatusOK, `{"username":"frodi"}`, verify.Valid},
		{"accepted with an unfamiliar body", http.StatusOK, `<html>hello</html>`, verify.Valid},
		{"accepted with an empty body", http.StatusOK, ``, verify.Valid},
		{"refused", http.StatusUnauthorized, `{"error":"unauthorized"}`, verify.Rejected},
		{"forbidden", http.StatusForbidden, ``, verify.Inconclusive},
		{"no whoami endpoint", http.StatusNotFound, ``, verify.Inconclusive},
		{"method not allowed", http.StatusMethodNotAllowed, ``, verify.Inconclusive},
		{"registry outage", http.StatusInternalServerError, ``, verify.Inconclusive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, host := server(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})

			if got := v.Verify(t.Context(), host, "tok"); got != tt.want {
				t.Errorf("outcome = %v, want %v", got, tt.want)
			}
		})
	}
}

// An unreadable body costs the username and nothing else.
func TestUnparseableBodyStillReportsValid(t *testing.T) {
	v, host := server(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json at all`))
	})

	user, outcome := v.Identify(t.Context(), host, "tok")

	if outcome != verify.Valid {
		t.Errorf("outcome = %v, want %v", outcome, verify.Valid)
	}
	if user != "" {
		t.Errorf("username = %q, want empty when the body cannot be read", user)
	}
}

func TestIdentifyReportsTheUsername(t *testing.T) {
	v, host := server(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"username":"frodi"}`))
	})

	user, outcome := v.Identify(t.Context(), host, "tok")

	if outcome != verify.Valid || user != "frodi" {
		t.Errorf("Identify = %q, %v, want frodi, valid", user, outcome)
	}
}

func TestProbeHitsWhoamiWithABearerToken(t *testing.T) {
	var path, auth string
	v, host := server(t, func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"username":"frodi"}`))
	})

	v.Verify(t.Context(), host, "tok")

	if path != "/-/whoami" {
		t.Errorf("path = %q, want /-/whoami", path)
	}
	if auth != "Bearer tok" {
		t.Errorf("Authorization = %q, want a bearer token", auth)
	}
}

// Callers skip the probe without a token. This is the backstop.
func TestNoTokenMeansNoAuthorizationHeader(t *testing.T) {
	var auth string
	v, host := server(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusUnauthorized)
	})

	v.Verify(t.Context(), host, "")

	if auth != "" {
		t.Errorf("Authorization = %q, want none", auth)
	}
}

// Being offline is not evidence about a token.
func TestUnreachableRegistryIsInconclusive(t *testing.T) {
	v := verify.HTTP{}

	// Port 1 is reserved and nothing listens there.
	if got := v.Verify(t.Context(), "127.0.0.1:1", "tok"); got != verify.Inconclusive {
		t.Errorf("outcome = %v, want %v", got, verify.Inconclusive)
	}
}

// A cancelled context must not be reported as a rejection either.
func TestCancelledContextIsInconclusive(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	v, host := server(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"username":"frodi"}`))
	})

	if got := v.Verify(ctx, host, "tok"); got != verify.Inconclusive {
		t.Errorf("outcome = %v, want %v", got, verify.Inconclusive)
	}
}
