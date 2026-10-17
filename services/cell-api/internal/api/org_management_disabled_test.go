// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Organization management turned off (SLUICIO_ORG_MANAGEMENT_DISABLED):
// the org's name and slug belong to whoever runs the instance, so neither
// an org admin nor an operator can change them from inside it.

package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOrgManagementDisabledRefusesRenames(t *testing.T) {
	h := &Handlers{OrgManagementDisabled: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for name, handler := range map[string]http.HandlerFunc{"org admin": h.updateOrg, "operator": h.updateOperatorOrg} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"name":"Renamed","slug":"renamed"}`))
		req.SetPathValue("id", "00000000-0000-0000-0000-000000000001")
		handler(rec, req)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "turned off") {
			t.Errorf("%s: want 409 turned off, got %d %s", name, rec.Code, rec.Body)
		}
	}
}
