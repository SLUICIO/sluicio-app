//go:build liveprobe

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Shows what the integration list and the unhealthy feed say about
// integrations that share a service, against a real cell. Built for the
// Airflow demo (github.com/syron/airflow-otel): every DAG is an
// integration on the same scheduler, and the question is whether one
// DAG's failure reaches its siblings.
//
// With SLICE_PROBE_APPLY=<service>, first applies the "airflow" system
// type's checks to that service, through the same handler the
// POST /services/{name}/apply-template endpoint runs.
//
//	STATE_PROBE_PG='postgres://controlplane:controlplane@localhost:5433/controlplane' \
//	STATE_PROBE_CH=localhost:9000 \
//	go test -tags liveprobe ./services/cell-api/internal/api/ -run TestSliceStatusLive -v

package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/catalog"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/erroracks"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/facetmappings"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/facetoverrides"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/metadata"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/monitoringtemplates"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/servicefacets"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/servicetypes"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/store"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/systemtypes"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/tags"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/tracecompletion"
)

func TestSliceStatusLive(t *testing.T) {
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
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{chAddr}, Auth: clickhouse.Auth{Database: "telemetry"}})
	if err != nil {
		t.Fatal(err)
	}
	integrationStore := integrations.NewStore(pg)
	h := &Handlers{
		ServiceFacets:       servicetypes.NewRegistry(),
		ServiceFacetsCustom: servicefacets.NewStore(pg),
		FacetMappings:       facetmappings.NewStore(pg),
		FacetOverrides:      facetoverrides.NewStore(pg),
		Resolver:            integrations.NewResolver(integrationStore, 5*time.Second),
		Logger:              slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Store:               store.New(conn),
		Integrations:        integrationStore,
		Catalog:             catalog.NewStore(pg),
		Alerts:              alerting.NewStore(pg),
		TraceCompletion:     tracecompletion.NewStore(pg),
		Tags:                tags.NewStore(pg),
		Metadata:            metadata.NewStore(pg),
		ErrorAcks:           erroracks.NewStore(pg),
		Templates:           monitoringtemplates.NewStore(pg),
		SystemTypes:         systemtypes.NewStore(pg),
	}
	admin := identity.Principal{Kind: identity.PrincipalUser, OrgID: integrations.DefaultOrgID, Role: identity.RoleAdmin}
	call := func(hf http.HandlerFunc, method, target, body string, pathValues ...string) []byte {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, target, rd).WithContext(middleware.WithPrincipal(ctx, admin))
		for i := 0; i+1 < len(pathValues); i += 2 {
			req.SetPathValue(pathValues[i], pathValues[i+1])
		}
		rec := httptest.NewRecorder()
		hf(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, target, rec.Code, rec.Body.String())
		}
		return rec.Body.Bytes()
	}

	if svc := os.Getenv("SLICE_PROBE_APPLY"); svc != "" {
		if path := os.Getenv("SLICE_PROBE_APPLY_YAML"); path != "" {
			// A system type file rather than the imported type: the
			// checks go through the same createTemplateChecks the
			// endpoint calls, idempotent by name.
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc systemTypeDoc
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			checks := make([]systemCheck, 0, len(doc.Checks))
			for _, d := range doc.Checks {
				checks = append(checks, customCheckToSystemCheck(docToCheck(d)))
			}
			req := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(middleware.WithPrincipal(ctx, admin))
			created, updated, skipped, err := h.createTemplateChecks(req, integrations.DefaultOrgID, svc, checks, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("applied %s to %s: created=%d updated=%d skipped=%d", path, svc, created, updated, skipped)
		} else {
			out := call(h.applyTemplate, http.MethodPost, "/api/v1/services/"+svc+"/apply-template", `{"kind":"airflow"}`, "name", svc)
			t.Logf("applied the airflow template to %s: %s", svc, out)
		}
	}

	var list struct {
		Integrations []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			TraceCount uint64 `json:"trace_count"`
			ErrorCount uint64 `json:"error_trace_count"`
		} `json:"integrations"`
	}
	if err := json.Unmarshal(call(h.listIntegrations, http.MethodGet, "/api/v1/integrations?range=1h", ""), &list); err != nil {
		t.Fatal(err)
	}
	t.Log("GET /api/v1/integrations?range=1h  (sluicio_list_integrations)")
	for _, i := range list.Integrations {
		if strings.HasPrefix(i.Name, "Airflow") {
			t.Logf("  %-32s %-9s traces=%d errors=%d", i.Name, i.Status, i.TraceCount, i.ErrorCount)
		}
	}

	var feed struct {
		Integrations []struct {
			Name          string `json:"name"`
			Status        string `json:"status"`
			FailingChecks []struct {
				RuleName  string `json:"rule_name"`
				OnService string `json:"on_service"`
			} `json:"failing_checks"`
			ErrorServices []struct {
				ServiceName string `json:"service_name"`
				ErrorTraces uint64 `json:"error_traces"`
			} `json:"error_services"`
		} `json:"integrations"`
		Other struct {
			FailingChecks []struct {
				RuleName  string `json:"rule_name"`
				OnService string `json:"on_service"`
			} `json:"failing_checks"`
		} `json:"other"`
	}
	if err := json.Unmarshal(call(h.unhealthyFeed, http.MethodGet, "/api/v1/unhealthy?range=1h", ""), &feed); err != nil {
		t.Fatal(err)
	}
	t.Log("GET /api/v1/unhealthy?range=1h  (sluicio_health)")
	for _, i := range feed.Integrations {
		if !strings.HasPrefix(i.Name, "Airflow") {
			continue
		}
		checks := make([]string, 0)
		for _, c := range i.FailingChecks {
			checks = append(checks, c.RuleName+"@"+c.OnService)
		}
		errs := make([]string, 0)
		for _, e := range i.ErrorServices {
			errs = append(errs, e.ServiceName)
		}
		t.Logf("  %-32s %-9s checks=%v errors=%v", i.Name, i.Status, checks, errs)
	}
	for _, c := range feed.Other.FailingChecks {
		t.Logf("  other: %s@%s", c.RuleName, c.OnService)
	}
}
