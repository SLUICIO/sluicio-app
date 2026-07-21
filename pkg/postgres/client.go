// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package postgres centralizes Postgres connection setup and provides
// a generic, fs.FS-backed migration runner. Each service embeds its
// own .sql files and asks the runner to apply them.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/sluicio/sluicio-app/pkg/env"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultDSN is the connection string used when POSTGRES_DSN is unset.
// It matches the Postgres started by docker-compose.yml.
const DefaultDSN = "postgres://controlplane:controlplane@localhost:5433/controlplane?sslmode=disable"

// PoolFromEnv reads POSTGRES_DSN (with DefaultDSN as fallback) and
// returns a connection pool that has been verified with Ping.
func PoolFromEnv(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := env.String("POSTGRES_DSN", DefaultDSN)
	return Pool(ctx, dsn)
}

// Pool opens a Postgres connection pool for the given DSN and verifies
// it with Ping.
func Pool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse dsn: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: open pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return pool, nil
}
