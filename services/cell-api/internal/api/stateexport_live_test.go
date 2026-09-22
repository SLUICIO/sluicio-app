//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The payload is unit-tested; the queries behind it are not, and they are
// the half that can be quietly wrong - a window read from the wrong end,
// a nil map, a status that disagrees with the page. This runs both
// gathering methods against a real cell.
//
//	STATE_PROBE_PG='postgres://controlplane:controlplane@localhost:5433/controlplane' \
//	STATE_PROBE_CH=localhost:9000 \
//	go test -tags liveprobe ./services/cell-api/internal/api/ -run TestStateExportLive -v

package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/stateexport"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/catalog"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/tracecompletion"
)

func TestStateExportLive(t *testing.T) {
	pgDSN, chAddr := os.Getenv("STATE_PROBE_PG"), os.Getenv("STATE_PROBE_CH")
	if pgDSN == "" || chAddr == "" {
		t.Skip("set STATE_PROBE_PG and STATE_PROBE_CH")
	}
	ctx := context.Background()
	pg, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{chAddr},
		Auth: clickhouse.Auth{Database: "telemetry"},
	})
	if err != nil {
		t.Fatal(err)
	}

	h := &Handlers{
		Logger:          slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Store:           store.New(conn),
		Integrations:    integrations.NewStore(pg),
		Catalog:         catalog.NewStore(pg),
		Alerts:          alerting.NewStore(pg),
		TraceCompletion: tracecompletion.NewStore(pg),
	}

	now := time.Now().UTC()
	states, err := h.IntegrationStates(ctx, integrations.DefaultOrgID, now.Add(-time.Hour), now, now.Add(-time.Minute), now)
	if err != nil {
		t.Fatalf("integration states: %v", err)
	}
	known := map[string]bool{"ok": true, "errors": true, "unhealthy": true, "quiet": true}
	for _, s := range states {
		t.Logf("integration %-28s %-9s messages=%d", s.Name, s.Status, s.Messages)
		if !known[s.Status] {
			t.Errorf("%s: %q is not one of the product's four states", s.Name, s.Status)
		}
		if s.Name == "" || s.ID.String() == "" {
			t.Errorf("a row without identity is unusable to a receiver: %+v", s)
		}
	}

	systems, err := h.SystemStates(ctx, integrations.DefaultOrgID, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatalf("system states: %v", err)
	}
	for _, s := range systems {
		t.Logf("system      %-28s %-9s type=%s", s.Name, s.Status, s.Kind)
		if !known[s.Status] {
			t.Errorf("%s: %q is not one of the product's four states", s.Name, s.Status)
		}
		if s.Messages != 0 {
			t.Errorf("%s: a system reported %d messages", s.Name, s.Messages)
		}
	}
	if len(states) == 0 && len(systems) == 0 {
		t.Skip("the cell has no integrations or systems; nothing to check")
	}

	// And the whole way out: gather, render, post. The receiver here is
	// a plain OTLP endpoint, which is what the estate tool is.
	var payloads int
	var names []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		msg := &colmetricspb.ExportMetricsServiceRequest{}
		if err := proto.Unmarshal(body, msg); err != nil {
			t.Errorf("receiver could not parse the payload: %v", err)
		}
		payloads++
		for _, rm := range msg.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					names = append(names, m.Name)
				}
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp := stateexport.New(
		stateexport.Config{Endpoint: srv.URL, CellName: "probe", Environment: "dev", HealthWindow: time.Hour},
		liveSource{h: h},
		integrations.DefaultOrgID,
		h.Logger,
		nil,
	)
	if err := exp.ExportOnce(ctx, now); err != nil {
		t.Fatalf("export: %v", err)
	}
	if payloads != 1 {
		t.Fatalf("expected one payload at the receiver, got %d", payloads)
	}
	t.Logf("receiver got: %v", names)
}

// liveSource is the same adapter main.go wires, kept here so the probe
// exercises the real path rather than a hand-written payload.
type liveSource struct{ h *Handlers }

func (s liveSource) IntegrationStates(ctx context.Context, orgID uuid.UUID, hf, ht, cf, ct time.Time) ([]stateexport.Entity, error) {
	rows, err := s.h.IntegrationStates(ctx, orgID, hf, ht, cf, ct)
	return liveEntities(rows), err
}

func (s liveSource) SystemStates(ctx context.Context, orgID uuid.UUID, hf, ht time.Time) ([]stateexport.Entity, error) {
	rows, err := s.h.SystemStates(ctx, orgID, hf, ht)
	return liveEntities(rows), err
}

func liveEntities(rows []EntityState) []stateexport.Entity {
	out := make([]stateexport.Entity, 0, len(rows))
	for _, r := range rows {
		out = append(out, stateexport.Entity{ID: r.ID, Name: r.Name, Slug: r.Slug, Kind: r.Kind, Status: r.Status, Messages: r.Messages})
	}
	return out
}
