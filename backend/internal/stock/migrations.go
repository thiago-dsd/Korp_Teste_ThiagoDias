// Package stock implements the stock service: products and balances.
package stock

import "embed"

// MigrationsFS holds the SQL migrations of the stock database.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory of MigrationsFS holding the migrations.
const MigrationsDir = "migrations"
