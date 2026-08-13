package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConstantTimeEqual(t *testing.T) {
	if !constantTimeEqual("abc", "abc") {
		t.Error("equal strings must compare equal")
	}
	if constantTimeEqual("abc", "abd") {
		t.Error("different strings must not compare equal")
	}
	if constantTimeEqual("abc", "") {
		t.Error("non-empty vs empty must not compare equal")
	}
}

func TestRandomStringLength(t *testing.T) {
	s, err := randomString(32)
	if err != nil {
		t.Fatalf("randomString: %v", err)
	}
	if len(s) < 40 {
		t.Errorf("expected a reasonably long token, got %q", s)
	}
	a, _ := randomString(32)
	if a == s {
		t.Error("two random tokens must differ")
	}
}

func TestSecurityHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	rec := httptest.NewRecorder()
	securityHeaders(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for _, h := range []string{
		"Content-Security-Policy",
		"Referrer-Policy",
		"X-Content-Type-Options",
		"X-Frame-Options",
		"Permissions-Policy",
	} {
		if rec.Header().Get(h) == "" {
			t.Errorf("missing security header %s", h)
		}
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != cspHeader {
		t.Errorf("unexpected CSP %q", got)
	}
}

func TestSessionTokenHashRoundTrip(t *testing.T) {
	raw, hash, err := newSessionToken()
	if err != nil {
		t.Fatalf("newSessionToken: %v", err)
	}
	if raw == hash {
		t.Error("raw token and hash must never be equal")
	}
	if len(raw) < 32 {
		t.Errorf("raw token too short: %q", raw)
	}
}
