// Package migrations embeds the application-specific migration files.
// These are passed to db.SetAppMigrationsFS() before calling db.RunMigration().
//
// Migration naming convention:
//   - Use 4-digit prefix starting from 1000: 1000_name.up.sql, 1001_name.up.sql, etc.
//   - Engine migrations use 0001-0999, application migrations use 1000-9999.
//   - All migrations are applied in lexicographic order by ID.
package migrations

import "embed"

//go:embed *.up.sql
var FS embed.FS
