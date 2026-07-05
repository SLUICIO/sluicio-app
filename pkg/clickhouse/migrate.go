// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package clickhouse

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// Migrate applies the embedded ClickHouse migrations in lexical order.
// Already-applied migrations are recorded in a `schema_migrations`
// table inside the same database; each migration runs at most once.
//
// Statements within a single .sql file are separated by a `;` followed
// by a newline. This is good enough for our schema files; if we ever
// need to embed semicolons inside a statement, switch to a more
// careful parser.
func Migrate(ctx context.Context, conn driver.Conn) error {
	if err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version String,
			applied_at DateTime DEFAULT now()
		) ENGINE = MergeTree()
		ORDER BY version
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := loadAppliedVersions(ctx, conn)
	if err != nil {
		return err
	}

	entries, err := fs.ReadDir(embeddedMigrations, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)

	for _, name := range files {
		version := strings.TrimSuffix(name, ".sql")
		if _, ok := applied[version]; ok {
			continue
		}
		body, err := fs.ReadFile(embeddedMigrations, path.Join("migrations", name))
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if err := applyMigration(ctx, conn, version, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}

func loadAppliedVersions(ctx context.Context, conn driver.Conn) (map[string]struct{}, error) {
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	out := map[string]struct{}{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = struct{}{}
	}
	return out, rows.Err()
}

func applyMigration(ctx context.Context, conn driver.Conn, version, body string) error {
	// Split on ";\n" so that statements ending with a semicolon at the
	// end of a line are treated as boundaries.
	for _, stmt := range splitStatements(body) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("statement failed: %w\n--- statement ---\n%s", err, stmt)
		}
	}
	return conn.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", version)
}

func splitStatements(body string) []string {
	// Remove SQL line comments to avoid trailing-semicolon confusion.
	var clean strings.Builder
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		clean.WriteString(line)
		clean.WriteString("\n")
	}
	return strings.Split(clean.String(), ";\n")
}

// ErrNoMigrations is returned by Migrate when the embedded migrations
// directory is empty. It is exported so callers can decide whether
// that's actually an error in their context.
var ErrNoMigrations = errors.New("clickhouse: no embedded migrations found")
