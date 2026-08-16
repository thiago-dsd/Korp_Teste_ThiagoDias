package billing_test

import (
	"testing"

	"github.com/thiagodias/korp-invoices/internal/billing"
	"github.com/thiagodias/korp-invoices/internal/platform/postgres"
)

func TestEmbeddedMigrationsAreValid(t *testing.T) {
	migrations, err := postgres.LoadMigrations(billing.MigrationsFS, billing.MigrationsDir)
	if err != nil {
		t.Fatalf("LoadMigrations() returned error: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations were embedded, want at least one")
	}
	if migrations[0].Version != 1 {
		t.Errorf("first migration version = %d, want 1", migrations[0].Version)
	}
}
