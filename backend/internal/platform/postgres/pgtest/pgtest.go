// Package pgtest provides the PostgreSQL setup shared by integration tests.
package pgtest

import (
	"context"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thiagodias/korp-invoices/internal/platform/postgres"
)

// Pool connects to the database named by envVar, resets its schema and applies
// the given migrations. Tests are skipped when the variable is unset, so the
// suite still runs on machines without a database.
func Pool(t *testing.T, envVar string, migrations fs.FS, migrationsDir string) (context.Context, *pgxpool.Pool) {
	t.Helper()

	databaseURL := os.Getenv(envVar)
	if databaseURL == "" {
		t.Skipf("%s is not set; skipping integration test", envVar)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	pool, err := postgres.Connect(ctx, databaseURL, postgres.DefaultPoolOptions())
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset test schema: %v", err)
	}
	if err := postgres.Migrate(ctx, pool, migrations, migrationsDir); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return ctx, pool
}

// Truncate empties the given tables, so tests inside the same package start
// from a known state without paying for a full schema rebuild.
func Truncate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tables ...string) {
	t.Helper()

	for _, table := range tables {
		if _, err := pool.Exec(ctx, `TRUNCATE TABLE `+table+` RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}
