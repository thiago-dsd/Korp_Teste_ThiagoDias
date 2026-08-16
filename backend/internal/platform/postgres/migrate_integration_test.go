package postgres

import (
	"context"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// requirePostgres connects to the database in TEST_DATABASE_URL and resets its
// schema, skipping the test when no database is available (for example on a
// machine without Docker running).
func requirePostgres(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping PostgreSQL integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	pool, err := Connect(ctx, databaseURL, DefaultPoolOptions())
	if err != nil {
		t.Fatalf("Connect() returned error: %v", err)
	}
	t.Cleanup(pool.Close)

	// Each test starts from an empty schema so runs are repeatable.
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	return ctx, pool
}

func TestMigrateAppliesAndIsIdempotent(t *testing.T) {
	ctx, pool := requirePostgres(t)

	fsys := fstest.MapFS{
		"migrations/0001_create_widgets.sql": {Data: []byte(`CREATE TABLE widgets (id INTEGER PRIMARY KEY);`)},
		"migrations/0002_add_label.sql":      {Data: []byte(`ALTER TABLE widgets ADD COLUMN label TEXT NOT NULL DEFAULT '';`)},
	}

	if err := Migrate(ctx, pool, fsys, "migrations"); err != nil {
		t.Fatalf("Migrate() returned error: %v", err)
	}
	// Running again must not fail nor reapply anything.
	if err := Migrate(ctx, pool, fsys, "migrations"); err != nil {
		t.Fatalf("second Migrate() returned error: %v", err)
	}

	var appliedCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&appliedCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if appliedCount != 2 {
		t.Errorf("applied migrations = %d, want 2", appliedCount)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO widgets (id, label) VALUES (1, 'ok')`); err != nil {
		t.Errorf("schema is not usable after migration: %v", err)
	}
}

func TestMigrateStopsOnFailingMigration(t *testing.T) {
	ctx, pool := requirePostgres(t)

	fsys := fstest.MapFS{
		"migrations/0001_create_widgets.sql": {Data: []byte(`CREATE TABLE widgets (id INTEGER PRIMARY KEY);`)},
		"migrations/0002_broken.sql":         {Data: []byte(`THIS IS NOT SQL;`)},
	}

	if err := Migrate(ctx, pool, fsys, "migrations"); err == nil {
		t.Fatal("Migrate() returned no error for a broken migration, want one")
	}

	var appliedCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&appliedCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if appliedCount != 1 {
		t.Errorf("applied migrations = %d, want 1 (the failing one must not be recorded)", appliedCount)
	}
}

func TestConnectFailsOnInvalidURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := Connect(ctx, "not-a-database-url", DefaultPoolOptions()); err == nil {
		t.Fatal("Connect() returned no error for an invalid URL, want one")
	}
}
