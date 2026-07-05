// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ingest

import (
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/services/cell-ingest/internal/ingestauth"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// maxBody caps a single OTLP request body. OTel collectors typically
// batch well under this.
const maxBody = 64 << 20 // 64 MiB

// ── observability ────────────────────────────────────────────────────
//
// Every request emits a structured log line so an operator can see at a
// glance what's hitting the cell and what's being rejected. The
// previous version logged only ClickHouse failures and (at Debug)
// successes — leaving a 4-class blind spot:
//
//   - bad Content-Type → 415 (silent)
//   - bad gzip body    → 400 (silent)
//   - body too big     → 413 (silent)
//   - bad protobuf     → 400 (silent)
//
// All four return a clean HTTP error to the client but produced no
// log line on our side. A user with a misconfigured SDK could be
// hammering /v1/traces with JSON and we'd see nothing in journalctl.
//
// New shape:
//
//   - Every rejection logged at Warn with reason + remote addr +
//     content-type / -encoding so the cause is obvious.
//   - Every successful ingest logged at Info with the row count and
//     batch latency.
//   - Per-signal atomic counters (accepted / rejected / rows) so the
//     /healthz endpoint (or a future /metrics) can expose them
//     cheaply.
//
// Hot path overhead is one atomic increment + one slog call per
// request. slog with JSON encoding measures in microseconds at our
// expected request rates; cheaper than the protobuf unmarshal it
// follows.

// signalCounters is the atomic-counter set for one signal type.
// Inc-only, monotonic. Read by an exposed counter snapshot endpoint
// (not wired in this slice; reserved for the dogfood-self-instrument
// follow-up).
type signalCounters struct {
	accepted  atomic.Uint64
	rejected  atomic.Uint64
	rowsTotal atomic.Uint64
}

var (
	tracesCounters  signalCounters
	logsCounters    signalCounters
	metricsCounters signalCounters
)

// Counters returns a snapshot of all signal counters. Read-only;
// the returned struct is a point-in-time copy. Useful from /healthz
// and tests; future Prometheus-style /metrics endpoint will live here.
type Counters struct {
	Traces  CounterSnapshot `json:"traces"`
	Logs    CounterSnapshot `json:"logs"`
	Metrics CounterSnapshot `json:"metrics"`
}

// CounterSnapshot is a per-signal accepted / rejected / rows triple.
type CounterSnapshot struct {
	Accepted  uint64 `json:"accepted"`
	Rejected  uint64 `json:"rejected"`
	RowsTotal uint64 `json:"rows_total"`
}

// GetCounters reads the current values atomically.
func GetCounters() Counters {
	return Counters{
		Traces: CounterSnapshot{
			Accepted: tracesCounters.accepted.Load(),
			Rejected: tracesCounters.rejected.Load(),
			RowsTotal: tracesCounters.rowsTotal.Load(),
		},
		Logs: CounterSnapshot{
			Accepted: logsCounters.accepted.Load(),
			Rejected: logsCounters.rejected.Load(),
			RowsTotal: logsCounters.rowsTotal.Load(),
		},
		Metrics: CounterSnapshot{
			Accepted: metricsCounters.accepted.Load(),
			Rejected: metricsCounters.rejected.Load(),
			RowsTotal: metricsCounters.rowsTotal.Load(),
		},
	}
}

// rejectReason names the rejection cases we log. Keeping it a closed
// set means filters like `jq 'select(.reason=="bad_content_type")'`
// over JSON logs work reliably.
type rejectReason string

const (
	rejectBadContentType   rejectReason = "bad_content_type"
	rejectBadEncoding      rejectReason = "bad_gzip"
	rejectBodyReadFailed   rejectReason = "body_read_failed"
	rejectBodyTooBig       rejectReason = "body_too_big"
	rejectBadProto         rejectReason = "bad_protobuf"
	rejectClickHouseInsert rejectReason = "clickhouse_insert_failed"
	rejectUnauthorized     rejectReason = "unauthorized"
)

// remoteAddr extracts the client IP, honouring X-Forwarded-For so
// requests proxied through Caddy show the real source rather than the
// loopback. Caddy sets XFF by default for reverse_proxy routes.
func remoteAddr(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First IP in the list is the original client; subsequent
		// ones are proxy hops.
		for i, c := range xff {
			if c == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// logReject logs a rejected request and bumps the per-signal counter.
// Called from the readOTLPBody helper AND the per-handler proto-unmarshal
// branches. The signal name is passed by the caller because readOTLPBody
// runs before we know which signal we're on (it's the same code path
// for all three).
func logReject(logger *slog.Logger, r *http.Request, signal string, reason rejectReason, msg string) {
	counters := countersFor(signal)
	if counters != nil {
		counters.rejected.Add(1)
	}
	logger.Warn("ingest rejected",
		"signal", signal,
		"reason", string(reason),
		"remote", remoteAddr(r),
		"content_type", r.Header.Get("Content-Type"),
		"content_encoding", r.Header.Get("Content-Encoding"),
		"content_length", r.ContentLength,
		"user_agent", r.UserAgent(),
		"message", msg,
	)
}

// logAccept logs a successful ingest at Info level. Includes the row
// count and the wall-clock time from request entry to ClickHouse insert
// completion — that's the latency the upstream OTel exporter sees.
func logAccept(logger *slog.Logger, r *http.Request, signal string, rows int, took time.Duration) {
	counters := countersFor(signal)
	if counters != nil {
		counters.accepted.Add(1)
		if rows > 0 {
			counters.rowsTotal.Add(uint64(rows))
		}
	}
	logger.Info("ingest accepted",
		"signal", signal,
		"rows", rows,
		"remote", remoteAddr(r),
		"took_ms", took.Milliseconds(),
	)
}

func countersFor(signal string) *signalCounters {
	switch signal {
	case "traces":
		return &tracesCounters
	case "logs":
		return &logsCounters
	case "metrics":
		return &metricsCounters
	}
	return nil
}

// readOTLPBody validates an OTLP/HTTP request and returns its decoded
// (optionally gunzipped) protobuf body. JSON-encoded OTLP is not
// supported yet. On any error it has already written the response and
// returns ok=false, so the caller should simply return.
//
// `signal` is one of "traces" / "logs" / "metrics" — used for the
// reject-side logging so operators can grep by signal.
//
// The route patterns register the POST method, so method validation is
// handled by the mux.
func readOTLPBody(w http.ResponseWriter, r *http.Request, logger *slog.Logger, signal string) (body []byte, ok bool) {
	if r.Header.Get("Content-Type") != "application/x-protobuf" {
		logReject(logger, r, signal, rejectBadContentType, "only application/x-protobuf is supported")
		httpserver.WriteError(w, http.StatusUnsupportedMediaType,
			"only application/x-protobuf is supported in this version")
		return nil, false
	}

	var reader io.Reader = http.MaxBytesReader(w, r.Body, maxBody)
	if r.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(reader)
		if err != nil {
			logReject(logger, r, signal, rejectBadEncoding, err.Error())
			httpserver.WriteError(w, http.StatusBadRequest, "invalid gzip body")
			return nil, false
		}
		defer gr.Close()
		reader = gr
	}

	b, err := io.ReadAll(reader)
	if err != nil {
		// MaxBytesReader returns a *http.MaxBytesError when the limit
		// is hit. Distinguish 413 from 400 in the log so operators
		// can spot oversized-batch storms.
		if _, ok := err.(*http.MaxBytesError); ok {
			logReject(logger, r, signal, rejectBodyTooBig, err.Error())
			httpserver.WriteError(w, http.StatusRequestEntityTooLarge,
				"request body exceeds 64 MiB limit")
			return nil, false
		}
		logReject(logger, r, signal, rejectBodyReadFailed, err.Error())
		httpserver.WriteError(w, http.StatusBadRequest, "failed to read body")
		return nil, false
	}
	return b, true
}

// writeOTLPProto marshals an OTLP Export*ServiceResponse and writes it
// with the protobuf content type, per the OTLP/HTTP spec.
func writeOTLPProto(w http.ResponseWriter, msg proto.Message) {
	body, err := proto.Marshal(msg)
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "failed to encode response")
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ── ingest auth ──────────────────────────────────────────────────────

type orgCtxKey struct{}

// withOrgID stamps the resolved org id onto the request context.
func withOrgID(ctx context.Context, org string) context.Context {
	return context.WithValue(ctx, orgCtxKey{}, org)
}

// orgIDFromContext returns the org id stamped by RequireIngestKey ("" if
// absent — only possible when the middleware isn't wired).
func orgIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(orgCtxKey{}).(string)
	return v
}

// extractIngestKey reads the ingest key from "Authorization: Bearer …"
// (the OTLP-idiomatic header), falling back to X-Sluicio-Ingest-Key.
func extractIngestKey(r *http.Request) string {
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return strings.TrimSpace(r.Header.Get("X-Sluicio-Ingest-Key"))
}

// RequireIngestKey authenticates an OTLP request by per-org ingest key and
// stamps the resolved org on the request context (the handler copies it
// onto every row). Telemetry with no valid key is bounced with 401 —
// unless allowAnonymous is set (dev), in which case it's attributed to
// defaultOrg.
func RequireIngestKey(ks *ingestauth.KeyStore, allowAnonymous bool, defaultOrg, signal string, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if org, ok := ks.Resolve(r.Context(), extractIngestKey(r)); ok {
			next.ServeHTTP(w, r.WithContext(withOrgID(r.Context(), org.String())))
			return
		}
		if allowAnonymous {
			next.ServeHTTP(w, r.WithContext(withOrgID(r.Context(), defaultOrg)))
			return
		}
		logReject(logger, r, signal, rejectUnauthorized, "missing or invalid ingest API key")
		httpserver.WriteError(w, http.StatusUnauthorized, "missing or invalid ingest API key")
	})
}

// TracesHandler implements OTLP/HTTP traces at POST /v1/traces.
func TracesHandler(store *Store, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		body, ok := readOTLPBody(w, r, logger, "traces")
		if !ok {
			return
		}
		var req coltracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			logReject(logger, r, "traces", rejectBadProto, err.Error())
			httpserver.WriteError(w, http.StatusBadRequest, "failed to unmarshal OTLP request")
			return
		}
		rows := ConvertRequest(req.GetResourceSpans())
		org := orgIDFromContext(r.Context())
		for i := range rows {
			rows[i].OrganizationID = org
		}
		if len(rows) > 0 {
			if err := store.InsertSpans(r.Context(), rows); err != nil {
				// CH-insert failures still go through logReject so
				// the counter bumps + the same structured shape lands
				// in the journal as other rejections.
				logReject(logger, r, "traces", rejectClickHouseInsert, err.Error())
				logger.Error("clickhouse insert failed", "signal", "traces", "err", err, "rows", len(rows))
				httpserver.WriteError(w, http.StatusInternalServerError, "failed to store spans")
				return
			}
		}
		logAccept(logger, r, "traces", len(rows), time.Since(start))
		writeOTLPProto(w, &coltracepb.ExportTraceServiceResponse{})
	})
}

// LogsHandler implements OTLP/HTTP logs at POST /v1/logs.
func LogsHandler(store *Store, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		body, ok := readOTLPBody(w, r, logger, "logs")
		if !ok {
			return
		}
		var req collogspb.ExportLogsServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			logReject(logger, r, "logs", rejectBadProto, err.Error())
			httpserver.WriteError(w, http.StatusBadRequest, "failed to unmarshal OTLP request")
			return
		}
		rows := ConvertLogsRequest(req.GetResourceLogs())
		org := orgIDFromContext(r.Context())
		for i := range rows {
			rows[i].OrganizationID = org
		}
		if len(rows) > 0 {
			if err := store.InsertLogs(r.Context(), rows); err != nil {
				logReject(logger, r, "logs", rejectClickHouseInsert, err.Error())
				logger.Error("clickhouse insert failed", "signal", "logs", "err", err, "rows", len(rows))
				httpserver.WriteError(w, http.StatusInternalServerError, "failed to store logs")
				return
			}
		}
		logAccept(logger, r, "logs", len(rows), time.Since(start))
		writeOTLPProto(w, &collogspb.ExportLogsServiceResponse{})
	})
}

// MetricsHandler implements OTLP/HTTP metrics at POST /v1/metrics.
func MetricsHandler(store *Store, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		body, ok := readOTLPBody(w, r, logger, "metrics")
		if !ok {
			return
		}
		var req colmetricspb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			logReject(logger, r, "metrics", rejectBadProto, err.Error())
			httpserver.WriteError(w, http.StatusBadRequest, "failed to unmarshal OTLP request")
			return
		}
		rows := ConvertMetricsRequest(req.GetResourceMetrics())
		org := orgIDFromContext(r.Context())
		for i := range rows {
			rows[i].OrganizationID = org
		}
		if len(rows) > 0 {
			if err := store.InsertMetrics(r.Context(), rows); err != nil {
				logReject(logger, r, "metrics", rejectClickHouseInsert, err.Error())
				logger.Error("clickhouse insert failed", "signal", "metrics", "err", err, "rows", len(rows))
				httpserver.WriteError(w, http.StatusInternalServerError, "failed to store metrics")
				return
			}
		}
		logAccept(logger, r, "metrics", len(rows), time.Since(start))
		writeOTLPProto(w, &colmetricspb.ExportMetricsServiceResponse{})
	})
}
