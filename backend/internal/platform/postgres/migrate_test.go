package postgres

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadMigrationsOrdersByVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/0002_create_invoices.sql": {Data: []byte("CREATE TABLE invoices ();")},
		"migrations/0001_create_products.sql": {Data: []byte("CREATE TABLE products ();")},
		"migrations/0003_create_outbox.sql":   {Data: []byte("CREATE TABLE outbox ();")},
	}

	migrations, err := LoadMigrations(fsys, "migrations")
	if err != nil {
		t.Fatalf("LoadMigrations() returned error: %v", err)
	}

	want := []struct {
		version int
		name    string
	}{
		{1, "create_products"},
		{2, "create_invoices"},
		{3, "create_outbox"},
	}
	if len(migrations) != len(want) {
		t.Fatalf("loaded %d migrations, want %d", len(migrations), len(want))
	}
	for i, expected := range want {
		if migrations[i].Version != expected.version || migrations[i].Name != expected.name {
			t.Errorf("migration %d = %d/%s, want %d/%s",
				i, migrations[i].Version, migrations[i].Name, expected.version, expected.name)
		}
		if migrations[i].SQL == "" {
			t.Errorf("migration %d has empty SQL", i)
		}
	}
}

func TestLoadMigrationsRejectsInvalidSets(t *testing.T) {
	tests := []struct {
		name       string
		fsys       fstest.MapFS
		wantDetail string
	}{
		{
			name: "bad file name",
			fsys: fstest.MapFS{
				"migrations/create_products.sql": {Data: []byte("SELECT 1;")},
			},
			wantDetail: "NNNN_name.sql",
		},
		{
			name: "duplicated version",
			fsys: fstest.MapFS{
				"migrations/0001_create_products.sql": {Data: []byte("SELECT 1;")},
				"migrations/0001_create_invoices.sql": {Data: []byte("SELECT 1;")},
			},
			wantDetail: "share version",
		},
		{
			name: "version gap",
			fsys: fstest.MapFS{
				"migrations/0001_create_products.sql": {Data: []byte("SELECT 1;")},
				"migrations/0003_create_invoices.sql": {Data: []byte("SELECT 1;")},
			},
			wantDetail: "no gaps",
		},
		{
			name: "does not start at one",
			fsys: fstest.MapFS{
				"migrations/0002_create_products.sql": {Data: []byte("SELECT 1;")},
			},
			wantDetail: "start at 1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadMigrations(tc.fsys, "migrations")
			if err == nil {
				t.Fatalf("LoadMigrations() returned no error, want one mentioning %q", tc.wantDetail)
			}
			if !strings.Contains(err.Error(), tc.wantDetail) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantDetail)
			}
		})
	}
}

func TestLoadMigrationsFailsOnMissingDir(t *testing.T) {
	if _, err := LoadMigrations(fstest.MapFS{}, "migrations"); err == nil {
		t.Fatal("LoadMigrations() returned no error for a missing directory, want one")
	}
}
