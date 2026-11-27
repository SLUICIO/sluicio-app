// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Collector management through the real auth chain against a real
// Postgres: who reads which team's collectors and who manages a team's
// tokens, for every kind of principal the cell knows. A collector's
// configuration can name a customer's internal endpoints, so the read
// scope is a secrecy boundary and must fail closed.
//
//	go test -tags integration ./services/cell-api/internal/api/ -run TestCollectorAuthz
package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

func TestCollectorAuthz(t *testing.T) {
	pool, ctx := newIsolationDB(t)
	idStore := identity.NewStore(pool)
	store := collectors.New(pool, nil)
	h := &Handlers{
		Identity:   idStore,
		Collectors: store,
		AuthMW:     &middleware.Resolver{Identity: idStore},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/collectors", h.listCollectors)
	mux.HandleFunc("GET /api/v1/collectors/{id}", h.getCollector)
	mux.HandleFunc("GET /api/v1/collector-teams", h.listCollectorTeams)
	mux.HandleFunc("GET /api/v1/settings/groups/{id}/collector-tokens", h.listCollectorTokens)
	mux.HandleFunc("POST /api/v1/settings/groups/{id}/collector-tokens", h.blockDemo(h.createCollectorToken))
	mux.HandleFunc("DELETE /api/v1/settings/groups/{id}/collector-tokens/{token}", h.blockDemo(h.revokeCollectorToken))
	mux.HandleFunc("DELETE /api/v1/settings/groups/{id}", h.AuthMW.RequireRole(identity.Role.CanAdmin, h.deleteGroupAdmin))
	mux.HandleFunc("DELETE /api/v1/collectors/{id}", h.blockDemo(h.deleteCollector))
	root := h.AuthMW.Wrap(nil, mux)

	org, err := idStore.CreateOrg(ctx, "Acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	other, err := idStore.CreateOrg(ctx, "Other", "other")
	if err != nil {
		t.Fatal(err)
	}
	group := func(orgID uuid.UUID, slug string) uuid.UUID {
		t.Helper()
		g, err := idStore.CreateGroup(ctx, orgID, identity.GroupInput{Name: slug, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		return g.ID
	}
	platform, payments, empty := group(org.ID, "platform"), group(org.ID, "payments"), group(org.ID, "empty")
	foreign := group(other.ID, "theirs")

	// One collector per team, the foreign one in another organization.
	collector := func(orgID, groupID uuid.UUID) uuid.UUID {
		t.Helper()
		_, tok, err := store.CreateToken(ctx, orgID, groupID, "seed", nil)
		if err != nil {
			t.Fatal(err)
		}
		id, _, err := store.Observe(ctx, collectors.Observation{OrgID: orgID, GroupID: groupID, TokenID: tok.ID, InstanceUID: uuid.NewString(), Transport: "websocket"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	cPlatform, cPayments, cForeign := collector(org.ID, platform), collector(org.ID, payments), collector(other.ID, foreign)
	_, foreignTok, _ := store.CreateToken(ctx, other.ID, foreign, "theirs", nil)

	session := func(email string, role identity.Role, groups map[uuid.UUID]identity.Role) (string, uuid.UUID) {
		t.Helper()
		u, err := idStore.CreateUser(ctx, email, email)
		if err != nil {
			t.Fatal(err)
		}
		if err := idStore.AddMember(ctx, u.ID, org.ID, role); err != nil {
			t.Fatal(err)
		}
		for g, r := range groups {
			if err := idStore.AddGroupMember(ctx, u.ID, g, r); err != nil {
				t.Fatal(err)
			}
		}
		s, err := idStore.CreateSession(ctx, u.ID, time.Hour, "test")
		if err != nil {
			t.Fatal(err)
		}
		return "cookie:" + s.ID, u.ID
	}
	bearer := func(ownerType string, owner uuid.UUID, kind, scopeRole string) string {
		t.Helper()
		tok, err := identity.NewToken(kind)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := idStore.CreateAPIToken(ctx, ownerType, owner, "t", scopeRole, nil, tok); err != nil {
			t.Fatal(err)
		}
		return "bearer:" + tok.Plaintext
	}
	serviceAccount := func(name string, role identity.Role, scope identity.ServiceAccountScope, groups map[uuid.UUID]identity.Role) string {
		t.Helper()
		sa, err := idStore.CreateServiceAccount(ctx, org.ID, name, "", role, scope, nil)
		if err != nil {
			t.Fatal(err)
		}
		for g, r := range groups {
			if err := idStore.AddGroupServiceAccount(ctx, sa.ID, g, r); err != nil {
				t.Fatal(err)
			}
		}
		return bearer("service_account", sa.ID, identity.TokenKindServiceAccount, "")
	}

	admin, _ := session("admin@acme", identity.RoleAdmin, nil)
	teamAdmin, teamAdminID := session("platform-admin@acme", identity.RoleViewer, map[uuid.UUID]identity.Role{platform: identity.RoleAdmin})
	teamViewer, _ := session("payments-viewer@acme", identity.RoleViewer, map[uuid.UUID]identity.Role{payments: identity.RoleViewer})
	noTeam, _ := session("nobody@acme", identity.RoleViewer, nil)
	orgEditor, _ := session("editor@acme", identity.RoleEditor, nil)
	readPAT := bearer("user", teamAdminID, identity.TokenKindPersonal, string(identity.RoleViewer))
	scopedSA := serviceAccount("scoped", identity.RoleViewer, identity.SAScopeScoped, map[uuid.UUID]identity.Role{payments: identity.RoleViewer})
	orgWideViewerSA := serviceAccount("orgwide-viewer", identity.RoleViewer, identity.SAScopeOrgWide, nil)
	orgWideAdminSA := serviceAccount("orgwide-admin", identity.RoleAdmin, identity.SAScopeOrgWide, nil)

	do := func(who, method, path, body string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if strings.HasPrefix(who, "cookie:") {
			req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: strings.TrimPrefix(who, "cookie:")})
		} else {
			req.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(who, "bearer:"))
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		root.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	listed := func(who string) map[uuid.UUID]bool {
		t.Helper()
		code, body := do(who, "GET", "/api/v1/collectors", "")
		if code != 200 {
			t.Fatalf("list: %d %s", code, body)
		}
		var resp struct {
			Collectors []struct {
				ID uuid.UUID `json:"id"`
			} `json:"collectors"`
		}
		_ = json.Unmarshal([]byte(body), &resp)
		out := map[uuid.UUID]bool{}
		for _, c := range resp.Collectors {
			out[c.ID] = true
		}
		return out
	}

	t.Run("reads follow team membership", func(t *testing.T) {
		cases := []struct {
			name string
			who  string
			want []uuid.UUID
		}{
			{"org admin reads every team", admin, []uuid.UUID{cPlatform, cPayments}},
			{"team admin reads their team", teamAdmin, []uuid.UUID{cPlatform}},
			{"team viewer reads their team", teamViewer, []uuid.UUID{cPayments}},
			{"no team reads nothing", noTeam, nil},
			{"org editor without a team reads nothing", orgEditor, nil},
			{"read-scoped PAT reads its owner's team", readPAT, []uuid.UUID{cPlatform}},
			{"scoped service account reads its team", scopedSA, []uuid.UUID{cPayments}},
			{"org-wide viewer service account reads nothing", orgWideViewerSA, nil},
			{"org-wide admin service account reads every team", orgWideAdminSA, []uuid.UUID{cPlatform, cPayments}},
		}
		for _, tc := range cases {
			got := listed(tc.who)
			if len(got) != len(tc.want) {
				t.Errorf("%s: sees %d collectors, want %d", tc.name, len(got), len(tc.want))
			}
			for _, id := range tc.want {
				if !got[id] {
					t.Errorf("%s: missing %s", tc.name, id)
				}
			}
			if got[cForeign] {
				t.Errorf("%s: sees another organization's collector", tc.name)
			}
		}
	})

	t.Run("direct reads outside the scope are 404", func(t *testing.T) {
		for _, tc := range []struct {
			who string
			id  uuid.UUID
		}{{teamViewer, cPlatform}, {teamAdmin, cPayments}, {noTeam, cPlatform}, {admin, cForeign}} {
			if code, _ := do(tc.who, "GET", "/api/v1/collectors/"+tc.id.String(), ""); code != http.StatusNotFound {
				t.Errorf("GET collector %s: %d, want 404", tc.id, code)
			}
		}
		if code, _ := do(teamViewer, "GET", "/api/v1/collectors/"+cPayments.String(), ""); code != 200 {
			t.Errorf("own collector: %d", code)
		}
	})

	t.Run("collector-teams lists and flags what the caller manages", func(t *testing.T) {
		manages := func(who string) map[uuid.UUID]bool {
			_, body := do(who, "GET", "/api/v1/collector-teams", "")
			var resp struct {
				Teams []collectorTeam `json:"teams"`
			}
			_ = json.Unmarshal([]byte(body), &resp)
			out := map[uuid.UUID]bool{}
			for _, tm := range resp.Teams {
				out[tm.ID] = tm.CanManage
			}
			return out
		}
		if m := manages(admin); len(m) != 3 || !m[platform] || !m[payments] || !m[empty] {
			t.Errorf("admin: %v", m)
		}
		if m := manages(teamAdmin); len(m) != 1 || !m[platform] {
			t.Errorf("team admin: %v", m)
		}
		if m := manages(teamViewer); len(m) != 1 || m[payments] {
			t.Errorf("team viewer must see its team without managing it: %v", m)
		}
		if m := manages(readPAT); m[platform] {
			t.Errorf("a read-scoped token must not manage tokens: %v", m)
		}
	})

	t.Run("token management", func(t *testing.T) {
		create := func(who string, g uuid.UUID) int {
			code, _ := do(who, "POST", "/api/v1/settings/groups/"+g.String()+"/collector-tokens", `{"name":"x"}`)
			return code
		}
		for _, tc := range []struct {
			name string
			who  string
			team uuid.UUID
			want int
		}{
			{"team admin mints for their team", teamAdmin, platform, http.StatusCreated},
			{"team admin cannot mint for another team", teamAdmin, payments, http.StatusNotFound},
			{"team viewer cannot mint", teamViewer, payments, http.StatusNotFound},
			{"read-scoped PAT cannot mint", readPAT, platform, http.StatusNotFound},
			{"scoped viewer service account cannot mint", scopedSA, payments, http.StatusNotFound},
			{"org admin mints for any team", admin, empty, http.StatusCreated},
			{"org admin cannot mint for another org's team", admin, foreign, http.StatusNotFound},
		} {
			if got := create(tc.who, tc.team); got != tc.want {
				t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
			}
		}
		if code, _ := do(teamAdmin, "GET", "/api/v1/settings/groups/"+payments.String()+"/collector-tokens", ""); code != http.StatusNotFound {
			t.Errorf("team admin listed another team's tokens: %d", code)
		}
		// Another organization's token id under one of our teams.
		if code, _ := do(admin, "DELETE", "/api/v1/settings/groups/"+platform.String()+"/collector-tokens/"+foreignTok.ID.String(), ""); code != http.StatusNotFound {
			t.Errorf("revoked another org's token: %d", code)
		}
		if _, err := store.Authenticate(ctx, "sct_x"); err == nil {
			t.Error("sanity: bogus token authenticated")
		}
	})

	t.Run("removing a collector", func(t *testing.T) {
		// Seed collectors are "connected"; mark the Platform one offline.
		if err := store.Disconnected(ctx, platform, mustInstanceUID(t, store, org.ID, cPlatform)); err != nil {
			t.Fatal(err)
		}
		if code, _ := do(teamViewer, "DELETE", "/api/v1/collectors/"+cPayments.String(), ""); code != http.StatusForbidden {
			t.Errorf("team viewer removed a collector: %d", code)
		}
		if code, _ := do(teamAdmin, "DELETE", "/api/v1/collectors/"+cPayments.String(), ""); code != http.StatusNotFound {
			t.Errorf("team admin reached another team's collector: %d", code)
		}
		if code, _ := do(admin, "DELETE", "/api/v1/collectors/"+cPayments.String(), ""); code != http.StatusConflict {
			t.Errorf("an online collector was removed: %d", code)
		}
		if code, body := do(teamAdmin, "DELETE", "/api/v1/collectors/"+cPlatform.String(), ""); code != http.StatusNoContent {
			t.Fatalf("team admin removing an offline collector: %d %s", code, body)
		}
		if code, _ := do(admin, "GET", "/api/v1/collectors/"+cPlatform.String(), ""); code != http.StatusNotFound {
			t.Errorf("removed collector still readable: %d", code)
		}
		// The team is now empty and can go.
		if code, body := do(admin, "DELETE", "/api/v1/settings/groups/"+platform.String(), ""); code != http.StatusNoContent {
			t.Errorf("deleting the emptied team: %d %s", code, body)
		}
	})

	t.Run("a team that owns collectors cannot be deleted", func(t *testing.T) {
		code, body := do(admin, "DELETE", "/api/v1/settings/groups/"+payments.String(), "")
		if code != http.StatusConflict || !strings.Contains(body, "collectors") {
			t.Fatalf("delete team with collectors: %d %s", code, body)
		}
		if code, body := do(admin, "DELETE", "/api/v1/settings/groups/"+empty.String(), ""); code != http.StatusNoContent {
			t.Fatalf("delete team without collectors: %d %s", code, body)
		}
	})
}

func mustInstanceUID(t *testing.T, store *collectors.Store, orgID, id uuid.UUID) string {
	t.Helper()
	c, err := store.Get(context.Background(), collectors.Scope{OrgID: orgID, All: true}, id)
	if err != nil {
		t.Fatal(err)
	}
	return c.InstanceUID
}
