// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// The usage an instance shows is measured against the limit that holds.
// A plan change reaches the deployment's limit (and restarts the
// instance) without reissuing the licence, so a customer moved from 25 to
// 50 kept reading "23 of 25" in the app while the portal said "23 of
// 50". The deployment's limit, which Postgres enforces, wins over the
// licence's advisory cap, and covers every organization on the instance.
package api

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/deploymentlimits"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

func TestUsageReadsTheDeploymentLimit(t *testing.T) {
	pool, ctx := newIsolationDB(t)
	idStore := identity.NewStore(pool)
	orgA, err := idStore.CreateOrg(ctx, "Customer", "customer")
	if err != nil {
		t.Fatal(err)
	}
	orgB, err := idStore.CreateOrg(ctx, "Customer test", "customer-test")
	if err != nil {
		t.Fatal(err)
	}
	for i, org := range []any{orgA.ID, orgA.ID, orgB.ID} {
		if _, err := pool.Exec(ctx, `INSERT INTO integrations (organization_id, slug, name) VALUES ($1, $2, $2)`, org, "int-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handlers{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Limits: deploymentlimits.New(pool)}
	req := httptest.NewRequest("GET", "/api/v1/usage", nil)
	req = req.WithContext(middleware.WithPrincipal(ctx, identity.Principal{OrgID: orgA.ID, Role: identity.RoleAdmin}))

	// No deployment limit and no licence cap: unlimited.
	if u := h.integrationUsage(req); !u.Unlimited {
		t.Errorf("no limits = %+v, want unlimited", u)
	}

	// The deployment's limit: the instance's whole count against it.
	h.MonitoredEntityLimit = 50
	u := h.integrationUsage(req)
	if u.Limit != 50 || u.Unlimited || u.Used != 3 || u.IntegrationCount != 3 || u.OverLimit {
		t.Errorf("with a deployment limit of 50 = %+v, want 3 of 50 across the instance", u)
	}
	h.MonitoredEntityLimit = 3
	if u := h.integrationUsage(req); !u.OverLimit {
		t.Errorf("at the deployment limit = %+v, want over the limit", u)
	}
}
