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

	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/collectors"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/identity"
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

	// Restoring an applied configuration is Enterprise: 402 without a licence.
	if code, _ := do(admin, "POST", base+"/deployments/"+depID.String()+"/rollback", nil); code != http.StatusPaymentRequired {
		t.Errorf("rollback without a licence: %d, want 402", code)
	}
}
