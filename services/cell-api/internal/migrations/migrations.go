// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package migrations embeds the cell-api's Postgres .sql files so the
// main binary can apply them at startup.
package migrations

import "embed"

//go:embed sql/*.sql
var FS embed.FS

// Dir is the subdirectory inside FS that contains the migration files.
const Dir = "sql"
