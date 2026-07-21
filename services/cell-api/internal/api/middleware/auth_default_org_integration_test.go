// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Regression test for the header-less active-org default (observed
// live on softronic-dev): the middleware used memberships[0], and
// ListMemberships orders alphabetically by org name — so adding the
// seeded admin to a new org named "CT Probe" silently flipped every
// header-less client they owned (PATs, MCP connectors, fresh browser
// sessions) from "Default" to "CT Probe", emptying every list. The
// default must be the OLDEST-JOINED membership and must not move when
// an alphabetically-earlier org is joined.
//
// Run with:
//
//	go test -tags integration ./services/cell-api/internal/api/middleware/...
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

func TestHeaderlessDefaultOrgIsStable(t *testing.T) {
	store, ctx := newGateDB(t)
	r := &middleware.Resolver{Identity: store}

	// The original setup: one user in the migration-seeded "Default"
	// org — exactly what the incident hit.
	orgs, err := store.ListOrgs(ctx)
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	var defaultOrg identity.Org
	for _, o := range orgs {
		if o.Slug == "default" {
			defaultOrg = o.Org
		}
	}
	if defaultOrg.ID == uuid.Nil {
		t.Fatal("no seeded default org")
	}
	user, err := store.CreateUser(ctx, "admin@seed", "admin@seed")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.AddMember(ctx, user.ID, defaultOrg.ID, identity.RoleAdmin); err != nil {
		t.Fatalf("add member: %v", err)
	}

	// The two header-less client kinds hit by the incident: a browser
	// session (cookie) and a personal access token.
	sess, err := store.CreateSession(ctx, user.ID, time.Hour, "test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	minted, err := identity.NewToken(identity.TokenKindPersonal)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	if _, err := store.CreateAPIToken(ctx, "user", user.ID, "cli", "", nil, minted); err != nil {
		t.Fatalf("create api token: %v", err)
	}

	resolve := func(t *testing.T, auth func(*http.Request), orgHeader string) identity.Principal {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		auth(req)
		if orgHeader != "" {
			req.Header.Set(middleware.HeaderActiveOrg, orgHeader)
		}
		p, ok, err := r.Resolve(req)
		if err != nil || !ok {
			t.Fatalf("resolve: ok=%v err=%v", ok, err)
		}
		return p
	}
	viaCookie := func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sess.ID})
	}
	viaPAT := func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+minted.Plaintext)
	}

	assertOrg := func(t *testing.T, got identity.Principal, want uuid.UUID, what string) {
		t.Helper()
		if got.OrgID != want {
			t.Fatalf("%s resolved to org %s, want %s", what, got.OrgID, want)
		}
	}

	// Baseline: sole membership resolves to Default on both paths.
	assertOrg(t, resolve(t, viaCookie, ""), defaultOrg.ID, "header-less cookie (baseline)")
	assertOrg(t, resolve(t, viaPAT, ""), defaultOrg.ID, "header-less PAT (baseline)")

	// The hijack: join an org whose name sorts BEFORE "Default".
	ctProbe, err := store.CreateOrg(ctx, "CT Probe", "ct-probe")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := store.AddMember(ctx, user.ID, ctProbe.ID, identity.RoleAdmin); err != nil {
		t.Fatalf("add member: %v", err)
	}

	// Header-less clients must STAY on the oldest-joined org.
	assertOrg(t, resolve(t, viaCookie, ""), defaultOrg.ID, "header-less cookie after joining CT Probe")
	assertOrg(t, resolve(t, viaPAT, ""), defaultOrg.ID, "header-less PAT after joining CT Probe")

	// Explicit pinning via X-Sluicio-Org still reaches the new org.
	assertOrg(t, resolve(t, viaCookie, "ct-probe"), ctProbe.ID, "cookie pinned to ct-probe")
	assertOrg(t, resolve(t, viaPAT, "ct-probe"), ctProbe.ID, "PAT pinned to ct-probe")
}
