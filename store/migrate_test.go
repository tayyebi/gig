package store

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tayyebi/gig/migrations"
)

func TestMigrateAppliesAndIsIdempotent(t *testing.T) {
	st := openTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := st.Migrate(ctx, migrations.FS); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := st.Migrate(ctx, migrations.FS); err != nil {
		t.Fatalf("second Migrate (no-op): %v", err)
	}

	var n int
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if n == 0 {
		t.Fatal("no migrations recorded")
	}
}

func TestMigrateRejectsChecksumChange(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.Migrate(ctx, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// A copy of the same migration with different content must be rejected.
	bad := fstest.MapFS{"0001_init.sql": &fstest.MapFile{Data: []byte("SELECT 1;\n")}}
	if err := st.Migrate(ctx, bad); err == nil {
		t.Fatal("expected checksum mismatch error")
	}
}

func TestMigrateAppliesNewMigrationsInOrder(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	t.Cleanup(func() {
		// Remove the temp migration rows and table so the test is repeatable
		// against a shared test database.
		_, _ = st.db.ExecContext(ctx, `DROP TABLE IF EXISTS a_tmp`)
		_, _ = st.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE name LIKE '%_tmp.sql'`)
	})

	fsys := fstest.MapFS{
		"9001_tmp.sql": &fstest.MapFile{Data: []byte("CREATE TABLE a_tmp (id int);\n")},
		"9002_tmp.sql": &fstest.MapFile{Data: []byte("ALTER TABLE a_tmp ADD COLUMN note text;\n")},
	}
	if err := st.Migrate(ctx, fsys); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var hasNote bool
	if err := st.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='a_tmp' AND column_name='note')`,
	).Scan(&hasNote); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !hasNote {
		t.Fatal("second migration was not applied")
	}
}

// TestMigrateUnderContention applies a fresh set of migrations from several
// goroutines at once. The advisory lock must serialize them and every call
// must succeed with no duplicate or partially-applied state.
func TestMigrateUnderContention(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = st.db.ExecContext(ctx, `DROP TABLE IF EXISTS a_tmp`)
		_, _ = st.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE name LIKE '%_tmp.sql'`)
	})

	fsys := fstest.MapFS{
		"9101_tmp.sql": &fstest.MapFile{Data: []byte("CREATE TABLE a_tmp (id int);\n")},
		"9102_tmp.sql": &fstest.MapFile{Data: []byte("ALTER TABLE a_tmp ADD COLUMN note text;\n")},
	}

	const workers = 5
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			errCh <- st.Migrate(ctx, fsys)
		}()
	}
	for i := 0; i < workers; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent Migrate: %v", err)
		}
	}

	// The temp migrations must appear exactly once each.
	for _, name := range []string{"9101_tmp.sql", "9102_tmp.sql"} {
		var n int
		if err := st.db.QueryRowContext(ctx,
			`SELECT count(*) FROM schema_migrations WHERE name = $1`, name,
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if n != 1 {
			t.Errorf("migration %s applied %d times, want 1", name, n)
		}
	}
}
