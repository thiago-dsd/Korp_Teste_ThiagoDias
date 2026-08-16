package stock_test

import (
	"testing"

	"github.com/thiagodias/korp-invoices/internal/platform/postgres"
	"github.com/thiagodias/korp-invoices/internal/stock"
)

func TestEmbeddedMigrationsAreValid(t *testing.T) {
	migrations, err := postgres.LoadMigrations(stock.MigrationsFS, stock.MigrationsDir)
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
