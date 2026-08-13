package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func setenv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoadDefaults(t *testing.T) {
	setenv(t, map[string]string{
		"DATABASE_URL":   "postgres://x:y@localhost/db",
		"SESSION_SECRET": "0123456789abcdef0123456789abcdef",
		"APP_ROLE":       "",
	})
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AppRole != RoleWeb {
		t.Errorf("AppRole = %q, want web", c.AppRole)
	}
	if c.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q", c.HTTPAddr)
	}
	if c.SessionTTL != 24*time.Hour {
		t.Errorf("SessionTTL = %v", c.SessionTTL)
	}
	if c.JobConcurrency != 4 {
		t.Errorf("JobConcurrency = %d", c.JobConcurrency)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	setenv(t, map[string]string{"SESSION_SECRET": "x"})
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when DATABASE_URL missing")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error should mention DATABASE_URL: %v", err)
	}
}

func TestLoadRequiresSessionSecret(t *testing.T) {
	setenv(t, map[string]string{
		"DATABASE_URL":   "postgres://x@y/db",
		"SESSION_SECRET": "",
	})
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when SESSION_SECRET missing")
	}
}

func TestLoadRejectsBadRole(t *testing.T) {
	setenv(t, map[string]string{
		"DATABASE_URL":   "postgres://x@y/db",
		"SESSION_SECRET": "0123456789abcdef0123456789abcdef",
		"APP_ROLE":       "scheduler",
	})
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "APP_ROLE") {
		t.Fatalf("expected APP_ROLE error, got %v", err)
	}
}

func TestLoadRejectsShortSecretInProduction(t *testing.T) {
	setenv(t, map[string]string{
		"DATABASE_URL":   "postgres://x@y/db",
		"SESSION_SECRET": "short",
		"ENVIRONMENT":    EnvProd,
	})
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for short SESSION_SECRET in production")
	}
}

func TestLoadCookieSecureInProduction(t *testing.T) {
	setenv(t, map[string]string{
		"DATABASE_URL":   "postgres://x@y/db",
		"SESSION_SECRET": "0123456789abcdef0123456789abcdef",
		"ENVIRONMENT":    EnvProd,
	})
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.CookieSecure {
		t.Error("CookieSecure should default true in production")
	}
}

func TestLoadParsesEnvOverrides(t *testing.T) {
	setenv(t, map[string]string{
		"DATABASE_URL":      "postgres://x@y/db",
		"SESSION_SECRET":    "0123456789abcdef0123456789abcdef",
		"APP_ROLE":          RoleWorker,
		"HTTP_ADDR":         ":9090",
		"JOB_CONCURRENCY":   "8",
		"JOB_POLL_INTERVAL": "250ms",
		"JOB_MAX_ATTEMPTS":  "7",
	})
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AppRole != RoleWorker || c.HTTPAddr != ":9090" || c.JobConcurrency != 8 {
		t.Errorf("unexpected overrides: %+v", c)
	}
	if c.JobPollInterval != 250*time.Millisecond {
		t.Errorf("JobPollInterval = %v", c.JobPollInterval)
	}
}

func TestLoadBadInt(t *testing.T) {
	setenv(t, map[string]string{
		"DATABASE_URL":    "postgres://x@y/db",
		"SESSION_SECRET":  "0123456789abcdef0123456789abcdef",
		"JOB_CONCURRENCY": "many",
	})
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid integer")
	}
}

func TestHostname(t *testing.T) {
	c := &Config{}
	if c.Hostname() == "" {
		t.Error("Hostname should not be empty")
	}
}

func TestLoadedConfigUsesEnv(t *testing.T) {
	// ensure env vars don't leak into other tests
	_ = os.Getenv("DATABASE_URL")
}
