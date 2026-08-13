package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tayyebi/gig/config"
)

// testConfig builds a config for integration tests, or returns nil if
// TEST_DATABASE_URL is not set.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database integration test")
	}
	return &config.Config{
		DatabaseURL:       dsn,
		DBMaxOpenConns:    4,
		DBMaxIdleConns:    2,
		DBConnMaxLifetime: time.Minute,
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), testConfig(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}
