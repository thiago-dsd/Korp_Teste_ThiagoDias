package identity

import "embed"

// MigrationsFS holds the SQL migrations of the identity database.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory of MigrationsFS holding the migrations.
const MigrationsDir = "migrations"
