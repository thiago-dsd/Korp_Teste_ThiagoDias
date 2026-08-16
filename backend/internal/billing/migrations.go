// Package billing implements the billing service: invoices and printing.
package billing

import "embed"

// MigrationsFS holds the SQL migrations of the billing database.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory of MigrationsFS holding the migrations.
const MigrationsDir = "migrations"
