// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate applies up-migrations from fsys/dir in lexical order. Applied
// versions are recorded in a `schema_migrations` table so each runs
// once. Only files ending in `.up.sql` are considered; the matching
// `.down.sql` files are ignored (downward migration is a separate
// operation we don't run automatically).
//
// The entire body of each .up.sql file is executed as a single
// statement inside a transaction. Postgres handles multi-statement
// strings well, so the schema files can contain several DDL statements
// separated by `;`.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, dir string) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := loadAppliedVersions(ctx, pool)
	if err != nil {
		return err
	}

	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("read migrations dir %q: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)

	for _, name := range files {
		version := strings.TrimSuffix(name, ".up.sql")
		if _, ok := applied[version]; ok {
			continue
		}
		body, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if err := applyMigration(ctx, pool, version, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}

func loadAppliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[string]struct{}, error) {
	rows, err := pool.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("select schema_migrations: %w", err)
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

func applyMigration(ctx context.Context, pool *pgxpool.Pool, version, body string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, body); err != nil {
		return fmt.Errorf("execute migration body: %w", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES ($1)", version); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	return tx.Commit(ctx)
}
