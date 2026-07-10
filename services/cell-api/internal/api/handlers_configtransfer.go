// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Config export & import (docs/config-transfer-design.md). Org-admin
// only, demo-blocked, audited, Community edition in both directions.
//
// The atomicity contract lives HERE: import runs in exactly one
// transaction — commit only on full success, rollback on any error, and
// dry_run rolls back unconditionally after producing the report. A
// failed or dry-run import leaves no trace beyond its audit entry.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/pkg/version"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/configtransfer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// exportConfig: GET /api/v1/settings/config-export
func (h *Handlers) exportConfig(w http.ResponseWriter, r *http.Request) {
	if h.PGPool == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	orgID := middleware.OrgID(r)
	tx, err := h.PGPool.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "could not start export")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	bundle, err := configtransfer.Export(r.Context(), tx, orgID.String(), version.Version)
	if err != nil {
		h.Logger.Error("config export failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "export failed")
		return
	}
	h.recordAudit(r, "config.exported", "org", orgID.String(), map[string]any{
		"sections": sectionCounts(bundle),
	})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="sluicio-config-%s-%s.json"`, bundle.Source.OrgSlug, time.Now().UTC().Format("2006-01-02")))
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(bundle)
}

// importConfig: POST /api/v1/settings/config-import?mode=strict|replace&dry_run=true|false&match_members_by_email=true
func (h *Handlers) importConfig(w http.ResponseWriter, r *http.Request) {
	if h.PGPool == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	p := middleware.Principal(r)
	if p.UserID == nil {
		httpserver.WriteError(w, http.StatusBadRequest, "config import requires a user session (imported dashboards and views need an owner)")
		return
	}
	var bundle configtransfer.Bundle
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20))
	if err := dec.Decode(&bundle); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid bundle JSON: "+err.Error())
		return
	}
	mode := configtransfer.Mode(r.URL.Query().Get("mode"))
	if mode == "" {
		mode = configtransfer.ModeStrict
	}
	dryRun := r.URL.Query().Get("dry_run") == "true"
	opts := configtransfer.Options{
		Mode:                mode,
		MatchMembersByEmail: r.URL.Query().Get("match_members_by_email") == "true",
		ActorUserID:         p.UserID.String(),
		IsOperator:          p.IsOperator,
	}
	orgID := middleware.OrgID(r)

	tx, err := h.PGPool.Begin(r.Context())
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "could not start import")
		return
	}
	// Rollback is always safe: it is a no-op after a successful commit.
	defer func() { _ = tx.Rollback(r.Context()) }()

	report := &configtransfer.Report{DryRun: dryRun}
	if err := configtransfer.Import(r.Context(), tx, orgID.String(), &bundle, opts, report); err != nil {
		// The deferred rollback erases everything — the failed import
		// never happened. Map the error honestly.
		var conflict *configtransfer.ConflictError
		var invalid *configtransfer.ValidationError
		var pgErr *pgconn.PgError
		switch {
		case errors.As(err, &conflict):
			httpserver.WriteError(w, http.StatusConflict, conflict.Error()+" — use mode=replace to overwrite")
		case errors.As(err, &invalid):
			httpserver.WriteError(w, http.StatusBadRequest, invalid.Error())
		case errors.As(err, &pgErr) && strings.HasPrefix(pgErr.Code, "23"):
			// Integrity violations are bundle-data problems (bad values,
			// broken uniqueness), not server bugs — and the rollback has
			// already erased everything.
			httpserver.WriteError(w, http.StatusBadRequest, "bundle violates a data constraint: "+pgErr.Message)
		default:
			h.Logger.Error("config import failed (rolled back)", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "import failed — nothing was changed")
		}
		return
	}

	if dryRun {
		// Same code path, no effects: roll back and hand over the report.
		_ = tx.Rollback(r.Context())
		httpserver.WriteJSON(w, http.StatusOK, report)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		h.Logger.Error("config import commit failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "import failed — nothing was changed")
		return
	}
	h.recordAudit(r, "config.imported", "org", orgID.String(), map[string]any{
		"mode": string(mode), "source_org": bundle.Source.OrgSlug, "report": report.Sections,
	})
	httpserver.WriteJSON(w, http.StatusOK, report)
}

func sectionCounts(b *configtransfer.Bundle) map[string]int {
	s := b.Sections
	return map[string]int{
		"tags": len(s.Tags), "metadata_fields": len(s.MetadataFields), "schemas": len(s.Schemas),
		"maps": len(s.Maps), "system_types": len(s.SystemTypes), "systems": len(s.Systems),
		"service_facets": len(s.ServiceFacets), "groups": len(s.Groups), "integrations": len(s.Integrations),
		"access_policies": len(s.AccessPolicies), "message_views": len(s.MessageViews),
		"monitoring_templates": len(s.Templates), "notification_channels": len(s.Channels),
		"notification_profiles": len(s.Profiles), "alert_rules": len(s.AlertRules),
		"dashboards": len(s.Dashboards), "cell_settings": len(s.CellSettings),
	}
}
