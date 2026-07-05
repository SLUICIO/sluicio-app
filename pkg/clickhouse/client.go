// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package clickhouse provides a small wrapper around the official
// ClickHouse Go driver. It centralizes connection setup so every
// service uses the same options, and exposes an in-process migration
// runner that applies the embedded schema files at startup.
package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Config describes how to connect to ClickHouse.
type Config struct {
	// Endpoint is the ClickHouse host[:port]. Defaults to 127.0.0.1:9000.
	Endpoint string
	// Database is the ClickHouse database name. Defaults to telemetry.
	Database string
	// Username is the ClickHouse user. Defaults to default.
	Username string
	// Password is the ClickHouse password. Defaults to empty.
	Password string
	// DialTimeout for opening the connection. Defaults to 5s.
	DialTimeout time.Duration
}

// ConfigFromEnv loads a ClickHouse config from the standard environment
// variables our services use:
//
//	CLICKHOUSE_ENDPOINT  host[:port]   (default 127.0.0.1:9000)
//	CLICKHOUSE_DATABASE  string        (default telemetry)
//	CLICKHOUSE_USERNAME  string        (default default)
//	CLICKHOUSE_PASSWORD  string
func ConfigFromEnv() Config {
	return Config{
		Endpoint:    envOr("CLICKHOUSE_ENDPOINT", "127.0.0.1:9000"),
		Database:    envOr("CLICKHOUSE_DATABASE", "telemetry"),
		Username:    envOr("CLICKHOUSE_USERNAME", "default"),
		Password:    os.Getenv("CLICKHOUSE_PASSWORD"),
		DialTimeout: 5 * time.Second,
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Open opens a ClickHouse connection and verifies it with a Ping. The
// returned driver.Conn can be used concurrently by multiple goroutines.
func Open(ctx context.Context, cfg Config) (driver.Conn, error) {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "127.0.0.1:9000"
	}
	if cfg.Database == "" {
		cfg.Database = "telemetry"
	}
	if cfg.Username == "" {
		cfg.Username = "default"
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}

	// clickhouse-go requires "host:port" — default the port if the caller
	// only supplied a host.
	if !strings.Contains(cfg.Endpoint, ":") {
		cfg.Endpoint = net.JoinHostPort(cfg.Endpoint, "9000")
	}

	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{cfg.Endpoint},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		DialTimeout: cfg.DialTimeout,
		Compression: &clickhouse.Compression{Method: clickhouse.CompressionLZ4},
		// Server-side guardrails on every query so a runaway can't
		// hold a worker indefinitely. The numbers are conservative
		// ceilings, not target costs — well-formed reads finish far
		// under each. A long-running maintenance task can override
		// via a per-query Settings map if needed later.
		Settings: clickhouse.Settings{
			"max_execution_time":     30,            // seconds wall clock
			"max_rows_to_read":       2_000_000_000, // 2B row hard cap
			"max_bytes_to_read":      50_000_000_000, // 50GB hard cap
			"read_overflow_mode":     "throw",
			"max_memory_usage":       2_000_000_000, // 2GB per-query
			"group_by_overflow_mode": "throw",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("clickhouse.Open: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
	defer cancel()
	if err := conn.Ping(pingCtx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clickhouse ping: %w", err)
	}

	return conn, nil
}

// EnsureDatabase issues CREATE DATABASE IF NOT EXISTS against a server
// connection (one connected without a Database). It is a convenience
// for local development where the target database may not yet exist.
//
// In production, Postgres-style provisioning should create the database
// up front via the operator or an init job.
func EnsureDatabase(ctx context.Context, cfg Config) error {
	bootstrapCfg := cfg
	bootstrapCfg.Database = "default"
	conn, err := Open(ctx, bootstrapCfg)
	if err != nil {
		return err
	}
	defer conn.Close()

	if cfg.Database == "" {
		return errors.New("clickhouse: target database name is empty")
	}
	return conn.Exec(ctx, "CREATE DATABASE IF NOT EXISTS "+quoteIdentifier(cfg.Database))
}

func quoteIdentifier(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}
