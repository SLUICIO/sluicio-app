// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// Acceptable Use Policy 4.3: at the plan's total no further integration
// or system is activated, and the refusal says where to upgrade.
func TestTheEntityLimitRefusalPointsToTheirPlan(t *testing.T) {
	h := &Handlers{MonitoredEntityLimit: 25, PlanURL: "https://portal.example.com/plan"}
	rec := httptest.NewRecorder()
	limitErr := fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23514", ConstraintName: "monitored_entity_limit"})
	if !h.refusedByEntityLimit(rec, limitErr) {
		t.Fatal("the limit error was not recognised")
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", rec.Code)
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		PlanURL string `json:"plan_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Error.Message, "at most 25") || !strings.Contains(body.Error.Message, "change plan at https://portal.example.com/plan") {
		t.Errorf("message %q does not give the limit and the way to upgrade", body.Error.Message)
	}
	if body.PlanURL != "https://portal.example.com/plan" {
		t.Errorf("plan_url %q", body.PlanURL)
	}
}
