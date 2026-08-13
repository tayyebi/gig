package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Migrate applies all pending embedded SQL migrations in lexical order under a
// PostgreSQL advisory lock. Applied migrations are recorded in the
// schema_migrations table; re-running is a no-op. A checksum mismatch for an
// already-applied migration is an error.
func (s *Store) Migrate(ctx context.Context, fsys fs.FS) error {
	conn, err := pgconn.Connect(ctx, s.dsn)
	if err != nil {
		return fmt.Errorf("connect for migration: %w", err)
	}
	defer conn.Close(ctx)

	if err := acquireLock(ctx, conn); err != nil {
		return err
	}
	defer releaseLock(conn)

	if err := ensureSchemaMigrations(ctx, conn); err != nil {
		return err
	}

	applied, err := appliedVersions(ctx, s)
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}

	files, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(files)

	for _, name := range files {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}

		content, err := fs.ReadFile(fsys, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		checksum := checksumHex(content)

		if rec, ok := applied[version]; ok {
			if rec.checksum != checksum {
				return fmt.Errorf("migration %s already applied with a different checksum (%s != %s)", name, rec.checksum, checksum)
			}
			continue
		}

		if err := applyMigration(ctx, conn, version, name, checksum, content); err != nil {
			return err
		}
	}
	return nil
}

const migrationTableSQL = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version    bigint PRIMARY KEY,
    name       text        NOT NULL,
    checksum   text        NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
)`

func acquireLock(ctx context.Context, conn *pgconn.PgConn) error {
	_, err := conn.Exec(ctx, fmt.Sprintf("SELECT pg_advisory_lock(%d)", advisoryLockKey)).ReadAll()
	if err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	return nil
}

func releaseLock(conn *pgconn.PgConn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = conn.Exec(ctx, fmt.Sprintf("SELECT pg_advisory_unlock(%d)", advisoryLockKey)).ReadAll()
}

func ensureSchemaMigrations(ctx context.Context, conn *pgconn.PgConn) error {
	_, err := conn.Exec(ctx, migrationTableSQL).ReadAll()
	if err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}
	return nil
}

type migrationRecord struct {
	version  int64
	checksum string
}

func appliedVersions(ctx context.Context, s *Store) (map[int64]migrationRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int64]migrationRecord)
	for rows.Next() {
		var rec migrationRecord
		if err := rows.Scan(&rec.version, &rec.checksum); err != nil {
			return nil, err
		}
		applied[rec.version] = rec
	}
	return applied, rows.Err()
}

func applyMigration(ctx context.Context, conn *pgconn.PgConn, version int64, name, checksum string, content []byte) error {
	if _, err := conn.Exec(ctx, "BEGIN").ReadAll(); err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.Exec(ctx, "ROLLBACK").ReadAll()
		}
	}()

	if _, err := conn.Exec(ctx, string(content)).ReadAll(); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if _, err := conn.Exec(ctx,
		fmt.Sprintf("INSERT INTO schema_migrations (version, name, checksum) VALUES (%d, '%s', '%s')",
			version, quoteLiteral(name), checksum),
	).ReadAll(); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if _, err := conn.Exec(ctx, "COMMIT").ReadAll(); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	committed = true
	return nil
}

func migrationVersion(name string) (int64, error) {
	base := strings.TrimSuffix(name, ".sql")
	idx := strings.IndexByte(base, '_')
	if idx < 0 {
		idx = len(base)
	}
	version, err := strconv.ParseInt(base[:idx], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("migration %s has an invalid version prefix: %w", name, err)
	}
	return version, nil
}

func checksumHex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func quoteLiteral(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
