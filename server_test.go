package main

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tayyebi/gig/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestStaticAssetsServed(t *testing.T) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}

	for _, path := range []string{"app.css", "favicon.ico", "favicon.svg"} {
		if _, err := fs.Stat(sub, path); err != nil {
			t.Errorf("static asset %s missing from embed: %v", path, err)
		}
	}

	s := newServer(&config.Config{BaseURL: "http://localhost"}, testLogger(), nil)
	mux := s.handler()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/app.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("static status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), ":root") {
		t.Error("app.css content not served")
	}
}

func TestStaticNotFound(t *testing.T) {
	s := newServer(&config.Config{BaseURL: "http://localhost"}, testLogger(), nil)
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/missing.txt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHealthzThroughMiddleware(t *testing.T) {
	s := newServer(&config.Config{BaseURL: "http://localhost"}, testLogger(), nil)
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// Security headers must be present on responses through the middleware.
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "script-src 'none'") {
		t.Errorf("CSP missing script-src 'none': %q", got)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing nosniff header")
	}
}
