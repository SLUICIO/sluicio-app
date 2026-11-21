// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Moving between organizations on a multi-org cell. A member switches
// among their own orgs with their own role in each; a cell operator, as
// a platform running the cell for many organizations, can open any org,
// and outside their memberships reads it as a viewer. Nobody else can
// name an org they do not belong to.
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

	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
)

func TestOperatorOpensAnyOrgAsViewer(t *testing.T) {
	store, ctx := newGateDB(t)
	r := &middleware.Resolver{Identity: store}

	partner, err := store.CreateOrg(ctx, "Partner", "partner")
	if err != nil {
		t.Fatal(err)
	}
	customer, err := store.CreateOrg(ctx, "Customer", "customer")
	if err != nil {
		t.Fatal(err)
	}

	newUser := func(email string, operator bool) string {
		t.Helper()
		u, err := store.CreateUser(ctx, email, email)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddMember(ctx, u.ID, partner.ID, identity.RoleAdmin); err != nil {
			t.Fatal(err)
		}
		if operator {
			if err := store.SetUserOperator(ctx, u.ID, true); err != nil {
				t.Fatal(err)
			}
		}
		sess, err := store.CreateSession(ctx, u.ID, time.Hour, "test")
		if err != nil {
			t.Fatal(err)
		}
		return sess.ID
	}
	resolve := func(session, org string) (identity.Principal, error) {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: session})
		req.Header.Set(middleware.HeaderActiveOrg, org)
		p, _, err := r.Resolve(req)
		return p, err
	}

	op := newUser("operator@partner.example", true)
	p, err := resolve(op, "customer")
	if err != nil {
		t.Fatalf("operator opening a non-member org: %v", err)
	}
	if p.OrgID != customer.ID || p.Role != identity.RoleViewer || !p.OperatorVisit {
		t.Errorf("operator in customer = org %s role %s visit %v, want customer as a visiting viewer", p.OrgID, p.Role, p.OperatorVisit)
	}
	// By id as well as by slug.
	if p, err := resolve(op, customer.ID.String()); err != nil || p.OrgID != customer.ID {
		t.Errorf("operator by org id: org %s err %v", p.OrgID, err)
	}
	// In an org they belong to, an operator keeps their own role.
	if p, err := resolve(op, "partner"); err != nil || p.Role != identity.RoleAdmin || p.OperatorVisit {
		t.Errorf("operator in own org = role %s visit %v err %v, want admin, not visiting", p.Role, p.OperatorVisit, err)
	}
	if _, err := resolve(op, "no-such-org"); err == nil {
		t.Error("an operator opened an org that does not exist")
	}

	// An org admin who is not an operator cannot reach another org.
	admin := newUser("admin@partner.example", false)
	if _, err := resolve(admin, "customer"); err == nil {
		t.Error("a non-operator opened an org they are not a member of")
	}
}
