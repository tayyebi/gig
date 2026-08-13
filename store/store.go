// Package store provides PostgreSQL data access through database/sql with the
// pgx driver. It owns SQL and transactions; it never renders HTML.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tayyebi/gig/config"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Store wraps a *sql.DB connection pool.
type Store struct {
	db  *sql.DB
	dsn string
}

// advisoryLockKey serializes migration runners so only one instance applies
// migrations at a time. It is independent of the configurable lock value.
const advisoryLockKey = int64(0x4749474D494731)

// Open connects to PostgreSQL and verifies the connection is usable.
func Open(ctx context.Context, cfg *config.Config) (*Store, error) {
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{db: db, dsn: cfg.DatabaseURL}, nil
}

// OpenWithRetry attempts to connect until the context is cancelled. It is used
// by the worker, which may start before the database has finished migrating.
func OpenWithRetry(ctx context.Context, cfg *config.Config, retry func(error)) (*Store, error) {
	for {
		st, err := Open(ctx, cfg)
		if err == nil {
			return st, nil
		}
		if retry != nil {
			retry(err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// DB exposes the underlying connection pool for queries.
func (s *Store) DB() *sql.DB { return s.db }

// Ping checks database connectivity.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close closes the connection pool.
func (s *Store) Close() error { return s.db.Close() }

// Tx runs fn inside a transaction, committing on success and rolling back on
// any error.
func (s *Store) Tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// isUniqueViolation reports whether err is a PostgreSQL unique constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
