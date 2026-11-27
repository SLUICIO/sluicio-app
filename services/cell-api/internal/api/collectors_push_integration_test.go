// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//go:build integration

// Sending a configuration to a collector through the real auth chain:
// who may send, to which collector, and that what is stored to send
// carries the real secrets the sender never saw.
//
//	go test -tags integration ./services/cell-api/internal/api/ -run TestCollectorPush
package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/license"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/advisor"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/proposals"
)

func TestCollectorPush(t *testing.T) {
	pool, ctx := newIsolationDB(t)
	idStore := identity.NewStore(pool)
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	keys, err := collectors.ParseKeys(base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatal(err)
	}
	store := collectors.New(pool, keys)
	h := &Handlers{
		Identity:   idStore,
		Collectors: store,
		AuthMW:     &middleware.Resolver{Identity: idStore},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/collectors/{id}", h.getCollector)
	mux.HandleFunc("POST /api/v1/collectors/{id}/deployments", h.blockDemo(h.createCollectorDeployment))
	mux.HandleFunc("GET /api/v1/collectors/{id}/deployments", h.listCollectorDeployments)
	mux.HandleFunc("GET /api/v1/collectors/{id}/deployments/{dep}/diff", h.collectorDeploymentDiff)
	mux.HandleFunc("POST /api/v1/collectors/{id}/deployments/{dep}/rollback", h.requireFeature("advisor", h.rollbackCollectorDeployment))
	root := h.AuthMW.Wrap(nil, mux)

	org, _ := idStore.CreateOrg(ctx, "Acme", "acme")
	team, _ := idStore.CreateGroup(ctx, org.ID, identity.GroupInput{Name: "platform", Slug: "platform"})
	other, _ := idStore.CreateGroup(ctx, org.ID, identity.GroupInput{Name: "payments", Slug: "payments"})
	_, tok, _ := store.CreateToken(ctx, org.ID, team.ID, "t", nil)

	supervisorCaps := uint64(1 | 2 | 4 | 2048)
	extensionCaps := uint64(1 | 4 | 2048)
	sup, _, _ := store.Observe(ctx, collectors.Observation{OrgID: org.ID, GroupID: team.ID, TokenID: tok.ID, InstanceUID: "sup", Transport: "websocket", Capabilities: &supervisorCaps, Description: map[string]string{"host.name": "gw-1"}})
	ext, _, _ := store.Observe(ctx, collectors.Observation{OrgID: org.ID, GroupID: team.ID, TokenID: tok.ID, InstanceUID: "ext", Transport: "websocket", Capabilities: &extensionCaps})
	stored := "exporters:\n  otlp_http:\n    endpoint: https://gw.example.test\n    headers:\n      authorization: Bearer real-secret\nreceivers:\n  host_metrics:\n    scrapers:\n      load: {}\nextensions:\n  opamp:\n    server:\n      ws:\n        endpoint: ws://127.0.0.1/v1/opamp\nservice:\n  extensions: [opamp]\n"
	if _, err := store.RecordConfig(ctx, sup, []collectors.ConfigFile{{Body: "effective: {}"}, {Name: collectors.RawConfigFile, Body: stored}}); err != nil {
		t.Fatal(err)
	}

	session := func(email string, role identity.Role, teamRole identity.Role, g uuid.UUID) string {
		u, _ := idStore.CreateUser(ctx, email, email)
		_ = idStore.AddMember(ctx, u.ID, org.ID, role)
		if teamRole != "" {
			_ = idStore.AddGroupMember(ctx, u.ID, g, teamRole)
		}
		s, _ := idStore.CreateSession(ctx, u.ID, time.Hour, "test")
		return s.ID
	}
	editor := session("editor@acme", identity.RoleViewer, identity.RoleEditor, team.ID)
	viewer := session("viewer@acme", identity.RoleViewer, identity.RoleViewer, team.ID)
	otherEditor := session("other@acme", identity.RoleViewer, identity.RoleEditor, other.ID)
	admin := session("admin@acme", identity.RoleAdmin, "", uuid.Nil)

	do := func(cookie, method, path string, body any) (int, map[string]any) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		}
		req := httptest.NewRequest(method, path, rd)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
		rec := httptest.NewRecorder()
		root.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// What the editor sees and edits: the masked configuration.
	masked, _ := collectors.MaskConfig(stored)
	edited := strings.Replace(masked, "      load: {}\n", "      load: {}\n      memory: {}\n", 1)
	base := "/api/v1/collectors/" + sup.String()

	if code, out := do(editor, "GET", base, nil); code != 200 || out["can_push"] != true {
		t.Fatalf("detail: %d can_push=%v", code, out["can_push"])
	}
	if code, out := do(viewer, "GET", base, nil); code != 200 || out["can_push"] != false {
		t.Fatalf("viewer detail: %d can_push=%v", code, out["can_push"])
	}
	for name, tc := range map[string]struct {
		who  string
		path string
		want int
	}{
		"team viewer refused":         {viewer, base, http.StatusForbidden},
		"another team's editor: 404":  {otherEditor, base, http.StatusNotFound},
		"read-only collector refused": {admin, "/api/v1/collectors/" + ext.String(), http.StatusConflict},
	} {
		if code, _ := do(tc.who, "POST", tc.path+"/deployments", map[string]any{"config": edited}); code != tc.want {
			t.Errorf("%s: %d, want %d", name, code, tc.want)
		}
	}

	code, out := do(editor, "POST", base+"/deployments", map[string]any{"config": edited, "message": "add memory scraper"})
	if code != http.StatusCreated {
		t.Fatalf("team editor sends: %d %v", code, out)
	}
	dep := out["deployment"].(map[string]any)
	depID := uuid.MustParse(dep["id"].(string))
	if dep["status"] != "pending" || out["delivered"] != false {
		t.Fatalf("no live agent: status %v delivered %v", dep["status"], out["delivered"])
	}
	_, body, err := store.DeploymentBody(ctx, sup, depID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "Bearer real-secret") || !strings.Contains(body, "memory: {}") {
		t.Fatalf("stored deployment lacks the real secret or the edit:\n%s", body)
	}
	if strings.Contains(body, "ws://127.0.0.1") || !strings.Contains(body, "opamp") {
		t.Fatalf("the supervisor's own extension was not handled:\n%s", body)
	}
	if strings.Contains(body, collectors.Masked) {
		t.Fatal("a masked placeholder would be sent")
	}

	// A masked value that cannot be traced back is refused.
	moved := strings.Replace(masked, "endpoint: https://gw.example.test", "endpoint: '"+collectors.Masked+"'", 1)
	if code, _ := do(editor, "POST", base+"/deployments", map[string]any{"config": moved}); code != http.StatusUnprocessableEntity {
		t.Errorf("untraceable masked value: %d, want 422", code)
	}

	// The diff a reviewer sees is masked on both sides.
	if code, out := do(viewer, "GET", base+"/deployments/"+depID.String()+"/diff", nil); code != 200 ||
		strings.Contains(out["after"].(string), "real-secret") || !strings.Contains(out["after"].(string), "memory: {}") {
		t.Fatalf("diff: %d %v", code, out)
	}

	// The collector answers; a second send supersedes nothing applied.
	if err := store.ReportStatus(ctx, sup, dep["hash"].(string), collectors.DeploymentApplied, ""); err != nil {
		t.Fatal(err)
	}
	_, list := do(viewer, "GET", base+"/deployments", nil)
	if got := list["deployments"].([]any)[0].(map[string]any)["status"]; got != "applied" {
		t.Fatalf("after the collector's answer: %v", got)
	}

	// A collector that reports only its effective configuration: nothing
	// to restore secrets from, so a masked value is refused, while a
	// complete file (what a CI pipeline sends) goes through.
	effOnly, _, _ := store.Observe(ctx, collectors.Observation{OrgID: org.ID, GroupID: team.ID, TokenID: tok.ID, InstanceUID: "eff", Transport: "websocket", Capabilities: &supervisorCaps})
	if _, err := store.RecordConfig(ctx, effOnly, []collectors.ConfigFile{{Body: stored}}); err != nil {
		t.Fatal(err)
	}
	effBase := "/api/v1/collectors/" + effOnly.String() + "/deployments"
	if code, out := do(editor, "POST", effBase, map[string]any{"config": masked}); code != http.StatusUnprocessableEntity {
		t.Errorf("a masked value against an effective-only configuration: %d %v, want 422", code, out)
	}
	if code, out := do(editor, "POST", effBase, map[string]any{"config": stored}); code != http.StatusCreated {
		t.Errorf("a complete configuration to an effective-only collector: %d %v, want 201", code, out)
	}

	// Restoring an applied configuration is Enterprise: 402 without a licence.
	if code, _ := do(admin, "POST", base+"/deployments/"+depID.String()+"/rollback", nil); code != http.StatusPaymentRequired {
		t.Errorf("rollback without a licence: %d, want 402", code)
	}
}

// The Enterprise half: a team that requires approval gets a proposal
// instead of a send, the author cannot approve it, someone else in the
// team can, a rejected one does not read as waiting, and an applied
// configuration can be restored.
func TestCollectorPushEnterprise(t *testing.T) {
	pool, ctx := newIsolationDB(t)
	idStore := identity.NewStore(pool)
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	keys, err := collectors.ParseKeys(base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatal(err)
	}
	store := collectors.New(pool, keys)
	h := &Handlers{
		Identity:   idStore,
		Collectors: store,
		Proposals:  proposals.NewStore(pool),
		License:    license.NewEntitledForTest(license.FeatureAdvisor),
		AuthMW:     &middleware.Resolver{Identity: idStore},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	h.Mount(mux)
	root := h.AuthMW.Wrap(nil, mux)

	org, _ := idStore.CreateOrg(ctx, "Acme", "acme")
	team, _ := idStore.CreateGroup(ctx, org.ID, identity.GroupInput{Name: "platform", Slug: "platform"})
	_, tok, _ := store.CreateToken(ctx, org.ID, team.ID, "t", nil)
	caps := uint64(1 | 2 | 4 | 2048)
	sup, _, _ := store.Observe(ctx, collectors.Observation{OrgID: org.ID, GroupID: team.ID, TokenID: tok.ID, InstanceUID: "sup", Transport: "websocket", Capabilities: &caps, Description: map[string]string{"host.name": "gw-1"}})
	stored := "exporters:\n  otlp_http:\n    endpoint: https://gw.example.test\n    headers:\n      authorization: Bearer real-secret\nreceivers:\n  host_metrics:\n    scrapers:\n      load: {}\nservice:\n  pipelines:\n    metrics:\n      receivers: [host_metrics]\n      exporters: [otlp_http]\n"
	if _, err := store.RecordConfig(ctx, sup, []collectors.ConfigFile{{Body: "effective: {}"}, {Name: collectors.RawConfigFile, Body: stored}}); err != nil {
		t.Fatal(err)
	}

	session := func(email string, role identity.Role, teamRole identity.Role) string {
		u, _ := idStore.CreateUser(ctx, email, email)
		_ = idStore.AddMember(ctx, u.ID, org.ID, role)
		if teamRole != "" {
			_ = idStore.AddGroupMember(ctx, u.ID, team.ID, teamRole)
		}
		s, _ := idStore.CreateSession(ctx, u.ID, time.Hour, "test")
		return s.ID
	}
	author := session("author@acme", identity.RoleViewer, identity.RoleEditor)
	colleague := session("colleague@acme", identity.RoleViewer, identity.RoleEditor)
	teamViewer := session("viewer@acme", identity.RoleViewer, identity.RoleViewer)
	admin := session("admin@acme", identity.RoleAdmin, "")
	orgEditor := session("org-editor@acme", identity.RoleEditor, "")

	do := func(cookie, method, path string, body any) (int, map[string]any) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
		rec := httptest.NewRecorder()
		root.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	base := "/api/v1/collectors/" + sup.String()
	settings := "/api/v1/settings/groups/" + team.ID.String() + "/collector-settings"
	masked, _ := collectors.MaskConfig(stored)
	edit := func(scraper string) string {
		return strings.Replace(masked, "      load: {}\n", "      load: {}\n      "+scraper+": {}\n", 1)
	}
	statusOf := func(depID string) (string, string) {
		_, list := do(teamViewer, "GET", base+"/deployments", nil)
		for _, d := range list["deployments"].([]any) {
			m := d.(map[string]any)
			if m["id"] == depID {
				ps, _ := m["proposal_state"].(string)
				return m["status"].(string), ps
			}
		}
		t.Fatalf("deployment %s not listed", depID)
		return "", ""
	}

	if code, _ := do(teamViewer, "PUT", settings, map[string]any{"require_approval": true}); code != http.StatusForbidden && code != http.StatusNotFound {
		t.Errorf("a team viewer changed the approval setting: %d", code)
	}
	if code, out := do(admin, "PUT", settings, map[string]any{"require_approval": true}); code != 200 {
		t.Fatalf("turn approval on: %d %v", code, out)
	}
	if _, out := do(author, "GET", base, nil); out["require_approval"] != true {
		t.Fatalf("detail does not say approval is required: %v", out["require_approval"])
	}

	// A send becomes a proposal; nothing is delivered.
	code, out := do(author, "POST", base+"/deployments", map[string]any{"config": edit("memory"), "message": "add memory"})
	if code != http.StatusAccepted || out["proposal"] == nil {
		t.Fatalf("send under approval: %d %v", code, out)
	}
	depID := out["deployment"].(map[string]any)["id"].(string)
	propID := out["proposal"].(map[string]any)["id"].(string)
	if st, ps := statusOf(depID); st != "proposed" || ps != "pending" {
		t.Fatalf("proposed deployment: %s/%s", st, ps)
	}
	if d, _, _ := store.NextToDeliver(ctx, sup); d != nil {
		t.Fatal("a proposed configuration would be delivered before approval")
	}
	// The proposal itself never holds the configuration.
	_, prop := do(admin, "GET", "/api/v1/proposals/"+propID, nil)
	if b, _ := json.Marshal(prop); strings.Contains(string(b), "real-secret") || strings.Contains(string(b), "memory: {}") {
		t.Fatalf("the proposal carries the configuration: %s", b)
	}

	// The author cannot approve their own change; a team viewer cannot
	// approve at all; a colleague in the team can.
	if code, _ := do(author, "POST", "/api/v1/proposals/"+propID+"/approve", map[string]any{}); code == 200 {
		t.Fatal("the author approved their own change")
	}
	if code, _ := do(teamViewer, "POST", "/api/v1/proposals/"+propID+"/approve", map[string]any{}); code == 200 {
		t.Fatal("a team viewer approved a change")
	}
	if code, _ := do(orgEditor, "POST", "/api/v1/proposals/"+propID+"/approve", map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("an org editor outside the team decided a collector proposal: %d", code)
	}
	if code, out := do(colleague, "POST", "/api/v1/proposals/"+propID+"/approve", map[string]any{}); code != 200 {
		t.Fatalf("a colleague approves: %d %v", code, out)
	}
	if st, _ := statusOf(depID); st != "pending" {
		t.Fatalf("approved deployment: %s, want pending", st)
	}
	d, body, _ := store.NextToDeliver(ctx, sup)
	if d == nil || d.ID.String() != depID || !strings.Contains(body, "Bearer real-secret") {
		t.Fatalf("the approved configuration is not the one to deliver: %v", d)
	}
	if err := store.ReportStatus(ctx, sup, d.Hash, collectors.DeploymentApplied, ""); err != nil {
		t.Fatal(err)
	}

	// A rejected proposal does not read as waiting.
	_, out = do(author, "POST", base+"/deployments", map[string]any{"config": edit("paging")})
	rejDep := out["deployment"].(map[string]any)["id"].(string)
	rejProp := out["proposal"].(map[string]any)["id"].(string)
	if code, out := do(colleague, "POST", "/api/v1/proposals/"+rejProp+"/reject", map[string]any{"note": "not now"}); code != 200 {
		t.Fatalf("reject: %d %v", code, out)
	}
	if st, ps := statusOf(rejDep); st != "proposed" || ps != "rejected" {
		t.Fatalf("rejected proposal: %s/%s", st, ps)
	}

	// Restore: send the applied configuration again, at once.
	code, out = do(author, "POST", base+"/deployments/"+depID+"/rollback", nil)
	if code != http.StatusCreated {
		t.Fatalf("restore: %d %v", code, out)
	}
	nd := out["deployment"].(map[string]any)
	if nd["rolled_back_from"] != depID || nd["via"] != "rollback" {
		t.Fatalf("restore deployment: %v", nd)
	}
	if code, _ := do(teamViewer, "POST", base+"/deployments/"+depID+"/rollback", nil); code != http.StatusForbidden {
		t.Errorf("a team viewer restored: %d", code)
	}
	if code, _ := do(author, "POST", base+"/deployments/"+rejDep+"/rollback", nil); code != http.StatusConflict {
		t.Errorf("restoring a configuration that never applied: %d, want 409", code)
	}
}

// An Advisor suggestion becomes a change to a chosen collector: the list
// says where it applies, the preview is the diff on the masked
// configuration, and sending it files a proposal for the owner team even
// though the team does not require approval.
func TestAdvisorSuggestionToCollector(t *testing.T) {
	pool, ctx := newIsolationDB(t)
	idStore := identity.NewStore(pool)
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	keys, err := collectors.ParseKeys(base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatal(err)
	}
	store := collectors.New(pool, keys)
	adv := advisor.NewStore(pool)
	h := &Handlers{
		Identity:   idStore,
		Collectors: store,
		Advisor:    adv,
		Proposals:  proposals.NewStore(pool),
		License:    license.NewEntitledForTest(license.FeatureAdvisor),
		AuthMW:     &middleware.Resolver{Identity: idStore},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	h.Mount(mux)
	root := h.AuthMW.Wrap(nil, mux)

	org, _ := idStore.CreateOrg(ctx, "Acme", "acme")
	team, _ := idStore.CreateGroup(ctx, org.ID, identity.GroupInput{Name: "platform", Slug: "platform"})
	_, tok, _ := store.CreateToken(ctx, org.ID, team.ID, "t", nil)
	sup := uint64(1 | 2 | 4 | 2048)
	ext := uint64(1 | 4 | 2048)
	observe := func(uid string, caps uint64) uuid.UUID {
		id, _, err := store.Observe(ctx, collectors.Observation{OrgID: org.ID, GroupID: team.ID, TokenID: tok.ID, InstanceUID: uid, Transport: "websocket", Capabilities: &caps, Description: map[string]string{"host.name": uid, "service.version": "0.166.0"}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	gw := observe("gw", sup)
	other := observe("other", sup)
	readOnly := observe("ro", ext)
	written := "receivers:\n  host_metrics:\n    scrapers:\n      load: {}\n  otlp:\n    protocols:\n      http: {}\nexporters:\n  otlp_http/sluicio:\n    endpoint: https://ingest.example.test\n    headers:\n      authorization: Bearer real-secret\n  debug: {}\nservice:\n  pipelines:\n    metrics:\n      receivers: [host_metrics]\n      exporters: [otlp_http/sluicio]\n    logs:\n      receivers: [otlp]\n      exporters: [otlp_http/sluicio]\n    traces:\n      receivers: [otlp]\n      exporters: [otlp_http/sluicio]\n"
	if _, err := store.RecordConfig(ctx, gw, []collectors.ConfigFile{{Body: "effective: {}"}, {Name: collectors.RawConfigFile, Body: written}}); err != nil {
		t.Fatal(err)
	}
	debugOnly := strings.Replace(written, "exporters: [otlp_http/sluicio]", "exporters: [debug]", 1)
	if _, err := store.RecordConfig(ctx, other, []collectors.ConfigFile{{Name: collectors.RawConfigFile, Body: debugOnly}}); err != nil {
		t.Fatal(err)
	}

	if err := adv.Upsert(ctx, org.ID, advisor.Suggestion{Fingerprint: "T1|metric|system.cpu.load_average.15m", Class: "T1", Advisor: "telemetry", ScopeKind: "metric", ScopeID: "system.cpu.load_average.15m", Title: `Nothing reads the "system.cpu.load_average.15m" metric`, Evidence: map[string]any{}, Weight: 1}); err != nil {
		t.Fatal(err)
	}
	if err := adv.Upsert(ctx, org.ID, advisor.Suggestion{Fingerprint: "T2|log|orders-api|DEBUG", Class: "T2", Advisor: "telemetry", ScopeKind: "service", ScopeID: "orders-api", Title: "Nobody has opened orders-api's DEBUG logs", Evidence: map[string]any{"severity_band": "DEBUG"}, Weight: 1}); err != nil {
		t.Fatal(err)
	}
	for _, sg := range []advisor.Suggestion{
		{Fingerprint: "T3|attr|orders-api|debug.blob", Class: "T3", ScopeKind: "attribute", ScopeID: "orders-api.debug.blob", Title: "dead weight"},
		{Fingerprint: "T4|attr|orders-api|order.id", Class: "T4", ScopeKind: "attribute", ScopeID: "orders-api.order.id", Title: "high cardinality"},
		{Fingerprint: "T6|pii|orders-api|customer.email", Class: "T6", ScopeKind: "attribute", ScopeID: "orders-api.customer.email", Title: "pii", Evidence: map[string]any{"referenced_by_config": false}},
		{Fingerprint: "T6|pii|orders-api|customer.id", Class: "T6", ScopeKind: "attribute", ScopeID: "orders-api.customer.id", Title: "pii referenced", Evidence: map[string]any{"referenced_by_config": true}},
	} {
		sg.Advisor, sg.Weight = "telemetry", 1
		if sg.Evidence == nil {
			sg.Evidence = map[string]any{}
		}
		if err := adv.Upsert(ctx, org.ID, sg); err != nil {
			t.Fatal(err)
		}
	}
	open, _ := adv.List(ctx, org.ID, "telemetry", "open", 10)
	ids := map[string]string{}
	for _, s := range open {
		if s.Title == "pii referenced" {
			ids["T6ref"] = s.ID.String()
		} else {
			ids[s.Class] = s.ID.String()
		}
	}

	session := func(email string, role identity.Role, teamRole identity.Role) string {
		u, _ := idStore.CreateUser(ctx, email, email)
		_ = idStore.AddMember(ctx, u.ID, org.ID, role)
		if teamRole != "" {
			_ = idStore.AddGroupMember(ctx, u.ID, team.ID, teamRole)
		}
		s, _ := idStore.CreateSession(ctx, u.ID, time.Hour, "test")
		return s.ID
	}
	admin := session("admin@acme", identity.RoleAdmin, "")
	editor := session("editor@acme", identity.RoleViewer, identity.RoleEditor)
	do := func(cookie, method, path string, body any) (int, map[string]any) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: cookie})
		rec := httptest.NewRecorder()
		root.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	base := "/api/v1/advisor/suggestions/" + ids["T1"] + "/collectors"

	// High cardinality is for review, and a key Sluicio's own
	// configuration reads is never hashed from here.
	for _, c := range []string{"T4", "T6ref"} {
		if code, out := do(admin, "GET", "/api/v1/advisor/suggestions/"+ids[c]+"/collectors", nil); code != http.StatusConflict {
			t.Errorf("%s: %d %v, want 409", c, code, out)
		}
	}
	// Attributes: a statement on the service's spans in the traces
	// pipeline; the spans themselves are never dropped.
	// Personal data is deleted by default; hashing is asked for.
	for c, want := range map[string]string{
		"T3":           `delete_key(attributes, "debug.blob") where resource.attributes["service.name"] == "orders-api"`,
		"T6":           `delete_key(attributes, "customer.email") where resource.attributes["service.name"] == "orders-api"`,
		"T6?hash=true": `set(attributes["customer.email"], SHA256(attributes["customer.email"]))`,
		"T3?hash=true": `delete_key(attributes, "debug.blob")`,
	} {
		class, query, _ := strings.Cut(c, "?")
		path := "/api/v1/advisor/suggestions/" + ids[class] + "/collectors/" + gw.String()
		if query != "" {
			path += "?" + query
		}
		code, out := do(admin, "GET", path, nil)
		cfg, _ := out["config"].(string)
		if code != 200 || !strings.Contains(cfg, want) || !strings.Contains(cfg, "transform/sluicio-advisor") {
			t.Fatalf("%s preview: %d %v", c, code, out)
		}
		if class == "T6" && query == "" && strings.Contains(cfg, "SHA256") {
			t.Fatal("the default personal-data change hashes instead of deleting")
		}
	}
	// Unread logs: the floor goes into the logs pipeline, records without
	// a severity kept.
	code, logPreview := do(admin, "GET", "/api/v1/advisor/suggestions/"+ids["T2"]+"/collectors/"+gw.String(), nil)
	if lp, _ := logPreview["config"].(string); code != 200 || !strings.Contains(lp, `resource.attributes["service.name"] == "orders-api" and severity_number > SEVERITY_NUMBER_UNSPECIFIED and severity_number < SEVERITY_NUMBER_INFO`) {
		t.Fatalf("log preview: %d %v", code, logPreview)
	}
	if code, _ := do(editor, "GET", base, nil); code != http.StatusForbidden {
		t.Errorf("a team editor read the advisor: %d", code)
	}
	code, out := do(admin, "GET", base, nil)
	if code != 200 {
		t.Fatalf("list: %d %v", code, out)
	}
	rows := map[string]map[string]any{}
	for _, r := range out["collectors"].([]any) {
		m := r.(map[string]any)
		rows[m["collector"].(map[string]any)["id"].(string)] = m
	}
	if _, listed := rows[readOnly.String()]; listed {
		t.Error("a read-only collector was offered")
	}
	if ch := rows[gw.String()]["changed"].([]any); len(ch) != 1 || ch[0] != "metrics" {
		t.Errorf("gw: %v", rows[gw.String()])
	}
	if rows[other.String()]["unavailable"] == nil {
		t.Errorf("a collector that does not send to Sluicio is offered as applicable: %v", rows[other.String()])
	}

	code, preview := do(admin, "GET", base+"/"+gw.String(), nil)
	after, _ := preview["config"].(string)
	if code != 200 || !strings.Contains(after, "filter/sluicio-advisor") || strings.Contains(after, "real-secret") || strings.Contains(preview["before"].(string), "real-secret") {
		t.Fatalf("preview: %d %v", code, preview)
	}

	// Sent with the suggestion: a proposal, though the team never asked
	// for approval, recorded as the Advisor's.
	dep := "/api/v1/collectors/" + gw.String() + "/deployments"
	if code, _ := do(editor, "POST", dep, map[string]any{"config": after, "suggestion_id": ids["T1"]}); code != http.StatusForbidden {
		t.Errorf("a team editor applied an Advisor suggestion: %d", code)
	}
	code, out = do(admin, "POST", dep, map[string]any{"config": after, "suggestion_id": ids["T1"]})
	if code != http.StatusAccepted || out["proposal"] == nil {
		t.Fatalf("advisor send: %d %v", code, out)
	}
	d := out["deployment"].(map[string]any)
	if d["via"] != "advisor" || !strings.HasPrefix(d["message"].(string), "Advisor: ") {
		t.Fatalf("deployment: %v", d)
	}
	propID := out["proposal"].(map[string]any)["id"].(string)
	if code, out := do(editor, "POST", "/api/v1/proposals/"+propID+"/approve", map[string]any{}); code != 200 {
		t.Fatalf("the owner team approves: %d %v", code, out)
	}
	next, body, _ := store.NextToDeliver(ctx, gw)
	if next == nil || !strings.Contains(body, "Bearer real-secret") || !strings.Contains(body, `name == "system.cpu.load_average.15m"`) {
		t.Fatalf("delivered body:\n%s", body)
	}
	if next.AdvisorClass != "T1" || next.AdvisorTarget == nil || next.AdvisorTarget.Metric != "system.cpu.load_average.15m" || next.AdvisorSuggestionID == nil || next.AdvisorSuggestionID.String() != ids["T1"] {
		t.Fatalf("the deployment does not record the suggestion: %+v", next)
	}
	// The team's approval is the decision the suggestion waited for.
	if s, _ := adv.Get(ctx, org.ID, uuid.MustParse(ids["T1"])); s.State != "accepted" {
		t.Fatalf("suggestion after approval: %s, want accepted", s.State)
	}

	// The effect: nothing to measure until the collector applied it.
	effect := "/api/v1/collectors/" + gw.String() + "/deployments/" + next.ID.String() + "/effect"
	if code, out := do(editor, "GET", effect, nil); code != 200 || out["applied"] != false || out["subject"] != "system.cpu.load_average.15m" {
		t.Fatalf("effect before applying: %d %v", code, out)
	}
	if err := store.ReportStatus(ctx, gw, next.Hash, collectors.DeploymentApplied, ""); err != nil {
		t.Fatal(err)
	}
	// Applied; this test has no ClickHouse, so the measurement itself is
	// the live probe's (advisor.TestMeasureMetricProbe).
	if code, _ := do(editor, "GET", effect, nil); code != http.StatusServiceUnavailable {
		t.Errorf("effect without telemetry storage: %d, want 503", code)
	}
	viewer := session("viewer@acme", identity.RoleViewer, identity.RoleViewer)
	if code, _ := do(viewer, "GET", effect, nil); code != http.StatusForbidden {
		t.Errorf("a team viewer read the effect: %d", code)
	}
}
