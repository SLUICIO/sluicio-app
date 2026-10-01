// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Deleting an integration or a system deletes the health checks bound to
// it, and nothing else.
//
// integration_id used to be ON DELETE SET NULL. The check survived with no
// scope at all, which every evaluator reads as "all services": a "ready
// messages > 1000" check written for one queue went on to watch every
// queue on every broker, and could fire on any of them.
//
//	go test -tags integration ./services/cell-api/internal/api/...

package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/catalog"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
)

func TestDeletingAnEntityDeletesItsBoundChecks(t *testing.T) {
	pool, ctx := newIsolationDB(t)
	idStore := identity.NewStore(pool)
	ints := integrations.NewStore(pool)
	h := &Handlers{
		Identity:     idStore,
		Integrations: ints,
		Catalog:      catalog.NewStore(pool),
		Resolver:     integrations.NewResolver(ints, time.Minute),
		AuthMW:       &middleware.Resolver{Identity: idStore},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	// Wired exactly as handlers.go wires them.
	mux.HandleFunc("DELETE /api/v1/integrations/{id}", h.writeAnywhere(h.requireManageIntegration(h.deleteIntegration)))
	mux.HandleFunc("GET /api/v1/integrations/{id}/delete-impact", h.writeAnywhere(h.requireManageIntegration(h.integrationDeleteImpact)))
	mux.HandleFunc("DELETE /api/v1/systems/{id}", h.writeAnywhere(h.requireManageSystem(h.deleteSystem)))
	mux.HandleFunc("GET /api/v1/systems/{id}/delete-impact", h.writeAnywhere(h.requireManageSystem(h.systemDeleteImpact)))

	org := createOrg(t, ctx, pool, "org-a", "Org A")
	mkInt := func(slug string) uuid.UUID {
		t.Helper()
		out, err := ints.Create(ctx, integrations.IntegrationWithMatchers{
			Integration: integrations.Integration{OrganizationID: org, Slug: slug, Name: slug},
			Matchers: []integrations.Matcher{{
				Attribute: "service.name", Operator: integrations.OperatorEquals, Value: "rabbitmq",
			}},
		})
		if err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
		return out.Integration.ID
	}
	orders := mkInt("orders-queue")
	invoices := mkInt("invoices-queue")
	broker := createSystem(t, ctx, pool, org, "broker-1")

	// A check, bound however the arguments say. The rules are written
	// directly so the test can also hold a legacy row the API would now
	// refuse (bound to an integration AND a system).
	mkCheck := func(name, service string, integ, system *uuid.UUID) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO alert_rules (organization_id, name, signal, rule_spec, service_name, integration_id, system_id)
			VALUES ($1, $2, 'metric', '{"metric":"rabbitmq_queue_messages_ready","op":">","threshold":1000}', NULLIF($3, ''), $4, $5)
			RETURNING id`, org, name, service, integ, system).Scan(&id); err != nil {
			t.Fatalf("create check %q: %v", name, err)
		}
		return id
	}
	ordersReady := mkCheck("orders: ready > 1000", "", &orders, nil)
	ordersAndService := mkCheck("orders: legacy service+integration", "rabbitmq", &orders, nil)
	ordersAndSystem := mkCheck("broker: legacy system+integration", "", &orders, &broker)
	invoicesReady := mkCheck("invoices: ready > 1000", "", &invoices, nil)
	brokerUp := mkCheck("broker: up", "", nil, &broker)
	serviceCheck := mkCheck("rabbitmq: up", "rabbitmq", nil, nil)
	global := mkCheck("anything: errors", "", nil, nil)

	u, err := idStore.CreateUser(ctx, "admin@example.test", "Admin")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := idStore.AddMember(ctx, u.ID, org, identity.RoleAdmin); err != nil {
		t.Fatalf("add member: %v", err)
	}
	sess, err := idStore.CreateSession(ctx, u.ID, time.Hour, "test")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	do := func(method, path string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sess.ID})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}
	type binding struct {
		exists      bool
		integration *uuid.UUID
		system      *uuid.UUID
	}
	check := func(id uuid.UUID) binding {
		t.Helper()
		var b binding
		err := pool.QueryRow(ctx, `SELECT integration_id, system_id FROM alert_rules WHERE id = $1`, id).Scan(&b.integration, &b.system)
		if err == nil {
			b.exists = true
		}
		return b
	}
	impact := func(path string) float64 {
		t.Helper()
		code, body := do(http.MethodGet, path)
		if code != http.StatusOK {
			t.Fatalf("GET %s: got %d, want 200", path, code)
		}
		n, _ := body["health_checks"].(float64)
		return n
	}

	t.Run("the confirmation counts what the integration delete will take", func(t *testing.T) {
		// The legacy system+integration row is the system's (system wins
		// wherever scope is resolved), so it is not counted here.
		if got := impact("/api/v1/integrations/" + orders.String() + "/delete-impact"); got != 2 {
			t.Fatalf("integration delete impact: got %v, want 2", got)
		}
	})

	t.Run("deleting an integration deletes its checks and leaves the rest", func(t *testing.T) {
		if code, _ := do(http.MethodDelete, "/api/v1/integrations/"+orders.String()); code != http.StatusNoContent {
			t.Fatalf("delete integration: got %d, want 204", code)
		}
		for name, id := range map[string]uuid.UUID{"bound": ordersReady, "bound with a service too": ordersAndService} {
			if check(id).exists {
				t.Errorf("%s check survived its integration: it would now evaluate over every service", name)
			}
		}
		if b := check(ordersAndSystem); !b.exists || b.integration != nil || b.system == nil || *b.system != broker {
			t.Errorf("system+integration check: got %+v, want it kept, bound to the system only", b)
		}
		for name, id := range map[string]uuid.UUID{
			"another integration's": invoicesReady, "the system's": brokerUp, "a service's": serviceCheck, "a global": global,
		} {
			if !check(id).exists {
				t.Errorf("%s check was deleted along with an integration it is not bound to", name)
			}
		}
	})

	t.Run("deleting a system deletes its checks and leaves the rest", func(t *testing.T) {
		if got := impact("/api/v1/systems/" + broker.String() + "/delete-impact"); got != 2 {
			t.Fatalf("system delete impact: got %v, want 2", got)
		}
		code, body := do(http.MethodDelete, "/api/v1/systems/"+broker.String())
		if code != http.StatusOK {
			t.Fatalf("delete system: got %d, want 200", code)
		}
		if n, _ := body["health_checks_deleted"].(float64); n != 2 {
			t.Errorf("delete system reported %v checks deleted, want 2", body["health_checks_deleted"])
		}
		if check(brokerUp).exists || check(ordersAndSystem).exists {
			t.Errorf("a system check survived its system")
		}
		if !check(invoicesReady).exists || !check(serviceCheck).exists || !check(global).exists {
			t.Errorf("a check not bound to the system was deleted with it")
		}
	})

	// The store is the only delete path today, but the schema must not
	// depend on that: a row deleted any other way still takes its checks.
	t.Run("the foreign key cascades on its own", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `DELETE FROM integrations WHERE id = $1`, invoices); err != nil {
			t.Fatalf("raw delete: %v", err)
		}
		if check(invoicesReady).exists {
			t.Fatalf("check survived a raw integration delete: integration_id still SET NULL, not CASCADE")
		}
	})
}
