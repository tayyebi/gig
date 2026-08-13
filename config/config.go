// Package config loads and validates runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	RoleWeb    = "web"
	RoleWorker = "worker"

	EnvDev  = "dev"
	EnvProd = "prod"
	EnvTest = "test"
)

// Config holds all runtime settings. Secrets are loaded from the environment
// and never written to disk, logs, or client output.
type Config struct {
	// Deployment
	AppRole     string
	Environment string
	HTTPAddr    string
	BaseURL     string
	LogLevel    string

	// Database
	DatabaseURL         string
	DBMaxOpenConns      int
	DBMaxIdleConns      int
	DBConnMaxLifetime   time.Duration
	MigrateAdvisoryLock int64

	// Sessions
	SessionCookieName string
	SessionTTL        time.Duration
	SessionSecret     string
	CSRFSecret        string
	CookieSecure      bool

	// Auth security
	AuthRateLimit    int
	AuthRateWindow   time.Duration
	AuthTokenTTL     time.Duration
	MaxLoginAttempts int
	LoginLockout     time.Duration
	TOTPSkew         int

	// Email
	EmailFrom string
	SMTPHost  string
	SMTPPort  string
	SMTPUser  string
	SMTPPass  string

	// Storage
	StorageDir     string
	UploadMaxBytes int64

	// Jobs
	JobPollInterval   time.Duration
	JobClaimTimeout   time.Duration
	JobMaxAttempts    int
	JobStaleThreshold time.Duration
	JobConcurrency    int

	// Lifecycle
	ShutdownTimeout time.Duration
}

// Load reads and validates configuration from the environment.
func Load() (*Config, error) {
	c := &Config{}

	var err error
	if c.AppRole, err = validatedRole(env("APP_ROLE", RoleWeb)); err != nil {
		return nil, err
	}
	c.Environment = env("ENVIRONMENT", EnvDev)
	if c.HTTPAddr = env("HTTP_ADDR", ":8080"); c.HTTPAddr == "" {
		return nil, errRequired("HTTP_ADDR")
	}
	if c.BaseURL = env("BASE_URL", "http://localhost:8080"); c.BaseURL == "" {
		return nil, errRequired("BASE_URL")
	}
	c.LogLevel = env("LOG_LEVEL", "info")

	if c.DatabaseURL = env("DATABASE_URL", ""); c.DatabaseURL == "" {
		return nil, errRequired("DATABASE_URL")
	}
	if c.DBMaxOpenConns, err = envInt("DB_MAX_OPEN_CONNS", 10); err != nil {
		return nil, err
	}
	if c.DBMaxIdleConns, err = envInt("DB_MAX_IDLE_CONNS", 5); err != nil {
		return nil, err
	}
	if c.DBConnMaxLifetime, err = envDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute); err != nil {
		return nil, err
	}
	if c.MigrateAdvisoryLock, err = envInt64("MIGRATE_ADVISORY_LOCK", 0x4749474D494731); err != nil {
		return nil, err
	}

	c.SessionCookieName = env("SESSION_COOKIE_NAME", "gig_session")
	if c.SessionTTL, err = envDuration("SESSION_TTL", 24*time.Hour); err != nil {
		return nil, err
	}
	if c.SessionSecret = env("SESSION_SECRET", ""); c.SessionSecret == "" {
		return nil, errRequired("SESSION_SECRET")
	}
	if c.Environment != EnvDev && len(c.SessionSecret) < 32 {
		return nil, fmt.Errorf("SESSION_SECRET must be at least 32 bytes in %s environment", c.Environment)
	}
	c.CSRFSecret = env("CSRF_SECRET", c.SessionSecret)
	if c.CSRFSecret == "" {
		return nil, errRequired("CSRF_SECRET")
	}
	if c.CookieSecure, err = envBool("COOKIE_SECURE", c.Environment == EnvProd); err != nil {
		return nil, err
	}

	if c.AuthRateLimit, err = envInt("AUTH_RATE_LIMIT", 10); err != nil {
		return nil, err
	}
	if c.AuthRateWindow, err = envDuration("AUTH_RATE_WINDOW", 15*time.Minute); err != nil {
		return nil, err
	}
	if c.AuthTokenTTL, err = envDuration("AUTH_TOKEN_TTL", 24*time.Hour); err != nil {
		return nil, err
	}
	if c.MaxLoginAttempts, err = envInt("MAX_LOGIN_ATTEMPTS", 5); err != nil {
		return nil, err
	}
	if c.LoginLockout, err = envDuration("LOGIN_LOCKOUT", 15*time.Minute); err != nil {
		return nil, err
	}
	if c.TOTPSkew, err = envInt("TOTP_SKEW", 1); err != nil {
		return nil, err
	}

	c.EmailFrom = env("EMAIL_FROM", "Gig <no-reply@example.com>")
	c.SMTPHost = env("SMTP_HOST", "")
	c.SMTPPort = env("SMTP_PORT", "587")
	c.SMTPUser = env("SMTP_USER", "")
	c.SMTPPass = env("SMTP_PASS", "")

	if c.StorageDir = env("STORAGE_DIR", "./data"); c.StorageDir == "" {
		return nil, errRequired("STORAGE_DIR")
	}
	if c.UploadMaxBytes, err = envInt64("UPLOAD_MAX_BYTES", 50<<20); err != nil {
		return nil, err
	}

	if c.JobPollInterval, err = envDuration("JOB_POLL_INTERVAL", time.Second); err != nil {
		return nil, err
	}
	if c.JobClaimTimeout, err = envDuration("JOB_CLAIM_TIMEOUT", 5*time.Minute); err != nil {
		return nil, err
	}
	if c.JobMaxAttempts, err = envInt("JOB_MAX_ATTEMPTS", 5); err != nil {
		return nil, err
	}
	if c.JobStaleThreshold, err = envDuration("JOB_STALE_THRESHOLD", 10*time.Minute); err != nil {
		return nil, err
	}
	if c.JobConcurrency, err = envInt("JOB_CONCURRENCY", 4); err != nil {
		return nil, err
	}

	if c.ShutdownTimeout, err = envDuration("SHUTDOWN_TIMEOUT", 15*time.Second); err != nil {
		return nil, err
	}

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) validate() error {
	if c.Environment == "" {
		return fmt.Errorf("ENVIRONMENT cannot be empty")
	}
	if c.DBMaxOpenConns < 1 {
		return fmt.Errorf("DB_MAX_OPEN_CONNS must be >= 1")
	}
	if c.JobMaxAttempts < 1 {
		return fmt.Errorf("JOB_MAX_ATTEMPTS must be >= 1")
	}
	if c.JobConcurrency < 1 {
		return fmt.Errorf("JOB_CONCURRENCY must be >= 1")
	}
	if c.UploadMaxBytes < 1 {
		return fmt.Errorf("UPLOAD_MAX_BYTES must be >= 1")
	}
	if c.AuthRateLimit < 1 {
		return fmt.Errorf("AUTH_RATE_LIMIT must be >= 1")
	}
	if c.MaxLoginAttempts < 1 {
		return fmt.Errorf("MAX_LOGIN_ATTEMPTS must be >= 1")
	}
	if c.TOTPSkew < 0 {
		return fmt.Errorf("TOTP_SKEW cannot be negative")
	}
	return nil
}

// IsProduction reports whether the runtime environment is production.
func (c *Config) IsProduction() bool { return c.Environment == EnvProd }

// Hostname returns the operating system hostname, used to tag job workers.
func (c *Config) Hostname() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "unknown"
}

func validatedRole(role string) (string, error) {
	switch role {
	case RoleWeb, RoleWorker:
		return role, nil
	default:
		return "", fmt.Errorf("APP_ROLE must be %q or %q, got %q", RoleWeb, RoleWorker, role)
	}
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func errRequired(key string) error {
	return fmt.Errorf("required environment variable %s is not set", key)
}

func envInt(key string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return n, nil
}

func envInt64(key string, def int64) (int64, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return n, nil
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration (e.g. 15s, 24h): %w", key, err)
	}
	return d, nil
}

func envBool(key string, def bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return b, nil
}
