// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Agents may now propose new configuration, not only tune a threshold:
// a new health check, a new trace-completion rule, an integration's
// settings. Filing stays open to anyone who can see the target, and
// approving replays the request a person would have made, as the
// reviewer, through the real router. So the route's own gates decide:
// a reviewer who could not have made the change by hand cannot approve
// it either.
//
//	go test -tags integration ./services/cell-api/internal/api/ -run Proposals

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/alerting"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/catalog"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/integrations"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/proposals"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/tracecompletion"
)

func TestProposalsThatAuthorConfiguration(t *testing.T) {
	pool, ctx := newIsolationDB(t)
	idStore := identity.NewStore(pool)
	ints := integrations.NewStore(pool)
	alerts := alerting.NewStore(pool)
	h := &Handlers{
		Identity:        idStore,
		Integrations:    ints,
		Alerts:          alerts,
		Proposals:       proposals.NewStore(pool),
		TraceCompletion: tracecompletion.NewStore(pool),
		Catalog:         catalog.NewStore(pool),
		Resolver:        integrations.NewResolver(ints, time.Minute),
		AuthMW:          &middleware.Resolver{Identity: idStore},
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	h.Mount(mux)
	server := h.AuthMW.Wrap(nil, mux)

	org := createOrg(t, ctx, pool, "org-a", "Org A")
	integ, err := ints.Create(ctx, integrations.IntegrationWithMatchers{
		Integration: integrations.Integration{OrganizationID: org, Slug: "orders", Name: "Orders"},
		Matchers:    []integrations.Matcher{{Attribute: "service.name", Operator: integrations.OperatorEquals, Value: "order-publisher"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	integID := integ.Integration.ID

	// Non-admins see what their team's policy grants; this team sees the
	// whole org, so visibility is not what these tests exercise.
	team, err := idStore.CreateGroup(ctx, org, identity.GroupInput{Name: "Everyone", Slug: "everyone"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idStore.CreatePolicy(ctx, team.ID, identity.AccessPolicyInput{Kind: identity.PolicyAllOrg}); err != nil {
		t.Fatal(err)
	}
	session := func(email string, role identity.Role) string {
		t.Helper()
		u, err := idStore.CreateUser(ctx, email, email)
		if err != nil {
			t.Fatal(err)
		}
		if err := idStore.AddMember(ctx, u.ID, org, role); err != nil {
			t.Fatal(err)
		}
		if role != identity.RoleAdmin {
			if err := idStore.AddGroupMember(ctx, u.ID, team.ID, identity.RoleViewer); err != nil {
				t.Fatal(err)
			}
		}
		s, err := idStore.CreateSession(ctx, u.ID, time.Hour, "test")
		if err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	admin := session("admin@example.test", identity.RoleAdmin)
	viewer := session("viewer@example.test", identity.RoleViewer)
	editor := session("editor@example.test", identity.RoleEditor)

	do := func(sess, method, path string, body any) (int, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sess})
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	file := func(sess string, body map[string]any) (int, map[string]any) {
		t.Helper()
		body["rationale"] = "seen in the telemetry"
		return do(sess, http.MethodPost, "/api/v1/proposals", body)
	}
	rulesNamed := func(name string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM alert_rules WHERE organization_id=$1 AND name=$2`, org, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	newCheck := map[string]any{
		"name": "Orders: no traffic", "severity": "warning", "signal": "trace",
		"integration_id":    integID.String(),
		"trace_volume_spec": map[string]any{"kind": alerting.TraceVolumeSpecKind, "threshold": 1, "window_seconds": 900},
	}

	t.Run("a new check is checked when filed", func(t *testing.T) {
		bad := map[string]any{"name": "", "signal": "trace"}
		if code, _ := file(viewer, map[string]any{"target_kind": proposalNewAlertRule, "changes": []any{map[string]any{"field": "rule", "after": bad}}}); code != http.StatusBadRequest {
			t.Fatalf("a rule the API would refuse is refused when filed: %d", code)
		}
	})

	var checkProposal string
	t.Run("a viewer files a new check, and only someone who may make it approves it", func(t *testing.T) {
		code, out := file(viewer, map[string]any{"target_kind": proposalNewAlertRule, "changes": []any{map[string]any{"field": "rule", "after": newCheck}}})
		if code != http.StatusCreated {
			t.Fatalf("file: %d %v", code, out)
		}
		checkProposal = out["id"].(string)
		if code, _ := file(viewer, map[string]any{"target_kind": proposalNewAlertRule, "changes": []any{map[string]any{"field": "rule", "after": newCheck}}}); code != http.StatusConflict {
			t.Fatalf("the same new check filed twice is one proposal: %d", code)
		}
		if code, _ := do(viewer, http.MethodPost, "/api/v1/proposals/"+checkProposal+"/approve", nil); code != http.StatusForbidden {
			t.Fatalf("a viewer cannot approve: %d", code)
		}
		if rulesNamed("Orders: no traffic") != 0 {
			t.Fatal("nothing is created before approval")
		}
		if code, out := do(admin, http.MethodPost, "/api/v1/proposals/"+checkProposal+"/approve", nil); code != http.StatusOK {
			t.Fatalf("approve: %d %v", code, out)
		}
		if rulesNamed("Orders: no traffic") != 1 {
			t.Fatal("approving created the check")
		}
	})

	t.Run("two new completion rules on one integration are two proposals", func(t *testing.T) {
		rule := func(name string) map[string]any {
			return map[string]any{"name": name, "severity": "warning", "enabled": true, "start_span_name": "process orders",
				"stages": []any{map[string]any{"spans": []any{map[string]any{"service": "erp-loader", "span_name": "Step.Send"}}, "timeout_seconds": 60}}}
		}
		a, outA := file(viewer, map[string]any{"target_kind": proposalNewCompletionRule, "target_id": integID.String(), "changes": []any{map[string]any{"field": "rule", "after": rule("ERP gets it")}}})
		b, _ := file(viewer, map[string]any{"target_kind": proposalNewCompletionRule, "target_id": integID.String(), "changes": []any{map[string]any{"field": "rule", "after": rule("WMS gets it")}}})
		if a != http.StatusCreated || b != http.StatusCreated {
			t.Fatalf("file: %d %d", a, b)
		}
		code, got := do(admin, http.MethodGet, "/api/v1/proposals/"+outA["id"].(string), nil)
		if code != http.StatusOK || got["proposal"].(map[string]any)["state"] != "pending" {
			t.Fatalf("the first is still pending after the second: %v", got)
		}
		// An editor may approve proposals, but creating a completion rule
		// by hand takes an admin, and the replayed request says so.
		if code, out := do(editor, http.MethodPost, "/api/v1/proposals/"+outA["id"].(string)+"/approve", nil); code != http.StatusBadRequest {
			t.Fatalf("an editor cannot approve what only an admin may create: %d %v", code, out)
		}
		if code, out := do(admin, http.MethodPost, "/api/v1/proposals/"+outA["id"].(string)+"/approve", nil); code != http.StatusOK {
			t.Fatalf("approve: %d %v", code, out)
		}
		rules, err := h.TraceCompletion.ListForIntegration(ctx, org, integID)
		if err != nil || len(rules) != 1 || rules[0].Spec.Stages[0].Spans[0].Service != "erp-loader" {
			t.Fatalf("the completion rule was created as proposed: %+v %v", rules, err)
		}
	})

	t.Run("integration settings: before is today's value, approval applies it", func(t *testing.T) {
		code, out := file(viewer, map[string]any{"target_kind": proposalIntegrationSetting, "target_id": integID.String(),
			"changes": []any{map[string]any{"field": "notify_unacknowledged_errors", "after": true}}})
		if code != http.StatusCreated {
			t.Fatalf("file: %d %v", code, out)
		}
		changes := out["changes"].([]any)
		if before := changes[0].(map[string]any)["before"]; before != false {
			t.Fatalf("before is the current value: %v", before)
		}
		if code, out := do(admin, http.MethodPost, "/api/v1/proposals/"+out["id"].(string)+"/approve", nil); code != http.StatusOK {
			t.Fatalf("approve: %d %v", code, out)
		}
		got, err := ints.Get(ctx, org, integID)
		if err != nil || !got.Integration.NotifyUnacknowledgedErrors {
			t.Fatalf("the setting is on: %v %v", got.Integration.NotifyUnacknowledgedErrors, err)
		}
		if code, _ := file(viewer, map[string]any{"target_kind": proposalIntegrationSetting, "target_id": integID.String(),
			"changes": []any{map[string]any{"field": "matchers", "after": []any{map[string]any{"attribute": "env", "operator": "equals", "value": "x"}}}}}); code != http.StatusBadRequest {
			t.Fatalf("matchers without a service are refused when filed: %d", code)
		}
	})

	t.Run("rule states and the options an agent may use", func(t *testing.T) {
		code, out := do(viewer, http.MethodGet, "/api/v1/alert-rules?integration="+integID.String(), nil)
		if code != http.StatusOK {
			t.Fatalf("list: %d", code)
		}
		rules := out["rules"].([]any)
		id := rules[0].(map[string]any)["id"].(string)
		st := out["states"].(map[string]any)[id].(map[string]any)
		if st["state"] != "ok" {
			t.Fatalf("a check that never fired is ok: %v", st)
		}
		ruleID := uuid.MustParse(id)
		if _, err := pool.Exec(ctx, `INSERT INTO alert_instances (alert_rule_id, state, fingerprint) VALUES ($1, 'firing', 'x')`, ruleID); err != nil {
			t.Fatal(err)
		}
		_, one := do(viewer, http.MethodGet, "/api/v1/alert-rules/"+id, nil)
		if s := one["state"].(map[string]any); s["state"] != "firing" || s["firing_instances"].(float64) != 1 {
			t.Fatalf("a firing instance makes it firing: %v", s)
		}

		if code, opts := do(viewer, http.MethodGet, "/api/v1/proposals/options", nil); code != http.StatusOK || len(opts["proposal_kinds"].([]any)) != 4 {
			t.Fatalf("options: %d %v", code, opts["proposal_kinds"])
		}
		if code, _ := do(viewer, http.MethodGet, "/api/v1/notification-routing", nil); code != http.StatusOK {
			t.Fatalf("routing: %d", code)
		}
	})
}
