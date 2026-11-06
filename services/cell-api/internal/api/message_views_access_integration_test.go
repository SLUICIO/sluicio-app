// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Who sees and who changes a saved view.
//
// A private view is its owner's, whatever their role: a viewer may keep
// their own, and nobody else sees it, an org admin included. Team and
// org-wide views follow the dashboard rule. Before this, no owner was
// recorded and the save dialog's "private" was shown to the whole org,
// while a viewer could not save at all.
//
//	go test -tags integration ./services/cell-api/internal/api/...

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

	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/messageviews"
)

type viewsFixture struct {
	t        *testing.T
	mux      *http.ServeMux
	admin    string // session cookies
	viewer   string
	member   string // viewer in the org, member of the team
	outsider string // viewer in the org, not in the team
	team     string // team id
}

func newViewsFixture(t *testing.T) *viewsFixture {
	t.Helper()
	pool, ctx := newIsolationDB(t)
	idStore := identity.NewStore(pool)
	h := &Handlers{
		Identity:     idStore,
		MessageViews: messageviews.NewStore(pool),
		AuthMW:       &middleware.Resolver{Identity: idStore},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	// No role gate on the routes, as in handlers.go: the handlers decide
	// per view. Require stands in for the session check every route has.
	mux.HandleFunc("GET /api/v1/message-views", h.AuthMW.Require(h.listMessageViews))
	mux.HandleFunc("POST /api/v1/message-views", h.AuthMW.Require(h.createMessageView))
	mux.HandleFunc("GET /api/v1/message-views/{id}", h.AuthMW.Require(h.getMessageView))
	mux.HandleFunc("PUT /api/v1/message-views/{id}", h.AuthMW.Require(h.updateMessageView))
	mux.HandleFunc("DELETE /api/v1/message-views/{id}", h.AuthMW.Require(h.deleteMessageView))

	orgs, err := idStore.ListOrgs(ctx)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("no seeded org: %v", err)
	}
	org := orgs[0].Org
	team, err := idStore.CreateGroup(ctx, org.ID, identity.GroupInput{Name: "Orders", Slug: "orders"})
	if err != nil {
		t.Fatalf("create team: %v", err)
	}

	session := func(email string, role identity.Role, inTeam bool) string {
		t.Helper()
		u, err := idStore.CreateUser(ctx, email, email)
		if err != nil {
			t.Fatalf("create %s: %v", email, err)
		}
		if err := idStore.AddMember(ctx, u.ID, org.ID, role); err != nil {
			t.Fatalf("add member: %v", err)
		}
		if inTeam {
			if err := idStore.AddGroupMember(ctx, u.ID, team.ID, identity.RoleViewer); err != nil {
				t.Fatalf("add team member: %v", err)
			}
		}
		sess, err := idStore.CreateSession(ctx, u.ID, time.Hour, "test")
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		return sess.ID
	}
	return &viewsFixture{
		t:        t,
		mux:      mux,
		admin:    session("admin@acme.test", identity.RoleAdmin, false),
		viewer:   session("viewer@acme.test", identity.RoleViewer, false),
		member:   session("member@acme.test", identity.RoleViewer, true),
		outsider: session("outsider@acme.test", identity.RoleViewer, false),
		team:     team.ID.String(),
	}
}

func (f *viewsFixture) do(method, path, cookie, body string) (int, []byte) {
	f.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// create saves a view and returns it, failing the test on any other
// status than want.
func (f *viewsFixture) create(cookie, body string, want int) MessageViewWire {
	f.t.Helper()
	code, raw := f.do(http.MethodPost, "/api/v1/message-views", cookie, body)
	if code != want {
		f.t.Fatalf("create %s: got %d (%s), want %d", body, code, raw, want)
	}
	var v MessageViewWire
	_ = json.Unmarshal(raw, &v)
	return v
}

func (f *viewsFixture) listed(cookie string) map[string]MessageViewWire {
	f.t.Helper()
	code, raw := f.do(http.MethodGet, "/api/v1/message-views", cookie, "")
	if code != http.StatusOK {
		f.t.Fatalf("list: got %d", code)
	}
	var body struct {
		Views []MessageViewWire `json:"views"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		f.t.Fatalf("list body: %v", err)
	}
	out := map[string]MessageViewWire{}
	for _, v := range body.Views {
		out[v.Name] = v
	}
	return out
}

func TestAViewerKeepsAPrivateViewThatOnlyTheySee(t *testing.T) {
	f := newViewsFixture(t)
	v := f.create(f.viewer, `{"name":"my orders","visibility":"private","filters":[]}`, http.StatusCreated)
	if v.Visibility != messageviews.VisibilityPrivate || !v.Mine || !v.CanManage {
		t.Fatalf("created view: %+v, want private, mine, manageable", v)
	}

	if _, ok := f.listed(f.viewer)["my orders"]; !ok {
		t.Error("the owner does not see their private view")
	}
	// Not even an org admin: private means the owner.
	if _, ok := f.listed(f.admin)["my orders"]; ok {
		t.Error("an org admin sees someone else's private view")
	}
	path := "/api/v1/message-views/" + v.ID
	if code, _ := f.do(http.MethodGet, path, f.admin, ""); code != http.StatusNotFound {
		t.Errorf("admin reading a private view: got %d, want 404", code)
	}
	if code, _ := f.do(http.MethodDelete, path, f.admin, ""); code != http.StatusNotFound {
		t.Errorf("admin deleting a private view: got %d, want 404", code)
	}

	if code, raw := f.do(http.MethodPut, path, f.viewer,
		`{"name":"my orders, renamed","pinned":true,"shared":true,"filters":[]}`); code != http.StatusOK {
		t.Fatalf("owner updating: got %d (%s)", code, raw)
	}
	// shared in an update body is ignored: visibility is fixed at create.
	if got := f.listed(f.viewer)["my orders, renamed"]; got.Visibility != messageviews.VisibilityPrivate {
		t.Errorf("after an update the view is %q, want private", got.Visibility)
	}
	if code, _ := f.do(http.MethodDelete, path, f.viewer, ""); code != http.StatusNoContent {
		t.Errorf("owner deleting: got %d, want 204", code)
	}
}

func TestAViewerCannotShareAViewWithTheOrgOrATeam(t *testing.T) {
	f := newViewsFixture(t)
	f.create(f.viewer, `{"name":"x","visibility":"org","filters":[]}`, http.StatusForbidden)
	// A team member with the viewer role may read the team's views, not
	// add to them.
	f.create(f.member, `{"name":"x","visibility":"team","groupId":"`+f.team+`","filters":[]}`, http.StatusForbidden)
	f.create(f.admin, `{"name":"x","visibility":"team","filters":[]}`, http.StatusBadRequest)
	f.create(f.admin, `{"name":"x","visibility":"everyone","filters":[]}`, http.StatusBadRequest)
}

func TestATeamViewIsSeenByItsTeamAndChangedByEditors(t *testing.T) {
	f := newViewsFixture(t)
	v := f.create(f.admin, `{"name":"orders team","visibility":"team","groupId":"`+f.team+`","filters":[]}`, http.StatusCreated)
	if v.Visibility != messageviews.VisibilityTeam || v.GroupID != f.team || v.GroupName != "Orders" {
		t.Fatalf("created view: %+v, want a team view of %s", v, f.team)
	}

	got, ok := f.listed(f.member)["orders team"]
	if !ok {
		t.Fatal("a team member does not see the team's view")
	}
	if got.CanManage {
		t.Error("a team member with the viewer role may manage the team's view")
	}
	if _, ok := f.listed(f.outsider)["orders team"]; ok {
		t.Error("someone outside the team sees the team's view")
	}
	if code, _ := f.do(http.MethodDelete, "/api/v1/message-views/"+v.ID, f.member, ""); code != http.StatusForbidden {
		t.Errorf("team viewer deleting the team's view: got %d, want 403", code)
	}
}

func TestAnOrgWideViewIsSeenByEveryoneAndChangedByEditors(t *testing.T) {
	f := newViewsFixture(t)
	// A client that predates visibility sends shared, which still means
	// org-wide.
	v := f.create(f.admin, `{"name":"all errors","shared":true,"filters":[]}`, http.StatusCreated)
	if v.Visibility != messageviews.VisibilityOrg {
		t.Fatalf("shared:true created a %q view, want org", v.Visibility)
	}
	got, ok := f.listed(f.viewer)["all errors"]
	if !ok {
		t.Fatal("a viewer does not see an org-wide view")
	}
	if got.CanManage || got.Mine {
		t.Errorf("viewer's copy: %+v, want neither mine nor manageable", got)
	}
	if code, _ := f.do(http.MethodPut, "/api/v1/message-views/"+v.ID, f.viewer,
		`{"name":"mine now","filters":[]}`); code != http.StatusForbidden {
		t.Errorf("viewer updating an org-wide view: got %d, want 403", code)
	}
}
