// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Command cell-ingest is the cell's OTLP receiver. It accepts OTLP/HTTP
// traces, logs, and metrics, converts them to internal rows, and writes
// them in batches to ClickHouse.
//
// JSON-encoded OTLP and the gRPC transport are not implemented yet;
// producers should send application/x-protobuf over HTTP.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	imclickhouse "github.com/sluicio/sluicio-app/pkg/clickhouse"
	"github.com/sluicio/sluicio-app/pkg/env"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/pkg/log"
	impostgres "github.com/sluicio/sluicio-app/pkg/postgres"
	"github.com/sluicio/sluicio-app/pkg/version"
	"github.com/sluicio/sluicio-app/services/cell-ingest/internal/ingest"
	"github.com/sluicio/sluicio-app/services/cell-ingest/internal/ingestauth"
)

const serviceName = "cell-ingest"

// defaultOrgID matches integrations.DefaultOrgID in cell-api — the org
// anonymous (dev) telemetry is attributed to when INGEST_ALLOW_ANONYMOUS
// is set. Duplicated as a literal because that const is internal to
// cell-api.
const defaultOrgID = "00000000-0000-0000-0000-000000000001"

func main() {
	logger := log.New(serviceName, log.FormatJSON)
	logger.Info("starting",
		"version", version.Version,
		"commit", version.Commit,
		"build_date", version.BuildDate,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := imclickhouse.ConfigFromEnv()
	if err := imclickhouse.EnsureDatabase(ctx, cfg); err != nil {
		logger.Error("ensure clickhouse database failed", "err", err)
		os.Exit(1)
	}
	conn, err := imclickhouse.Open(ctx, cfg)
	if err != nil {
		logger.Error("open clickhouse failed", "err", err)
		os.Exit(1)
	}
	defer conn.Close()

	if err := imclickhouse.Migrate(ctx, conn); err != nil {
		logger.Error("clickhouse migrate failed", "err", err)
		os.Exit(1)
	}
	logger.Info("clickhouse ready", "endpoint", cfg.Endpoint, "database", cfg.Database)

	store := ingest.NewStore(conn)

	// Per-org ingest auth. cell-ingest validates each OTLP batch's API
	// key against the ingest_keys table (written by cell-api) and stamps
	// the resolved org onto every row. Telemetry with no valid key is
	// bounced (401) unless INGEST_ALLOW_ANONYMOUS=true (dev), which
	// attributes it to the default org.
	pg, err := impostgres.PoolFromEnv(ctx)
	if err != nil {
		logger.Error("open postgres failed (needed for ingest key auth)", "err", err)
		os.Exit(1)
	}
	defer pg.Close()
	keys := ingestauth.New(pg, logger)
	go keys.Run(ctx, 0) // initial refresh + 30s cadence
	allowAnonymous := strings.EqualFold(os.Getenv("INGEST_ALLOW_ANONYMOUS"), "true")
	if allowAnonymous {
		logger.Warn("INGEST_ALLOW_ANONYMOUS=true — unauthenticated telemetry is accepted and attributed to the default org (dev only)")
	}

	wrap := func(signal string, h http.Handler) http.Handler {
		return ingest.RequireIngestKey(keys, allowAnonymous, defaultOrgID, signal, logger, h)
	}

	// Cell-level ingest flags (e.g. 5xx→Error span normalization),
	// TTL-cached so the hot path costs one PG read per 30s, not per batch.
	flags := ingest.NewCellFlags(pg, 0)

	mux := http.NewServeMux()
	mux.Handle("POST /v1/traces", wrap("traces", ingest.TracesHandler(store, flags, logger)))
	mux.Handle("POST /v1/logs", wrap("logs", ingest.LogsHandler(store, logger)))
	mux.Handle("POST /v1/metrics", wrap("metrics", ingest.MetricsHandler(store, logger)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		// Healthz doubles as a "is anything actually being received?"
		// view: returns per-signal accepted / rejected / row counters
		// alongside the status. Cheap (atomics), no DB hit. An operator
		// curl'ing this gets the answer to "is the cell live AND
		// receiving data?" in one call.
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{
			"status":   "ok",
			"counters": ingest.GetCounters(),
		})
	})

	addr := env.String("CELL_INGEST_ADDR", ":4318")
	if err := httpserver.Run(ctx, httpserver.Config{
		Addr:    addr,
		Handler: mux,
		Logger:  logger,
	}); err != nil {
		logger.Error("http server stopped with error", "err", err)
		os.Exit(1)
	}
	logger.Info("shutting down")
}
