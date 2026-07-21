// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Per-user UI preferences: tiny JSON blobs the frontend owns (column
// layout on the integrations list, view defaults, …), keyed per surface
// and scoped to (user, org). Deliberately dumb storage — the server
// validates the key shape and caps the size, nothing else. Preferences
// follow the account, so a layout set on one machine appears on the
// next; URL params still override per shared link.

package api

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
)

// prefKeyRe: namespaced lowercase keys like "integrations.columns".
var prefKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// prefMaxBytes caps a preference value — these are column lists, not
// documents.
const prefMaxBytes = 32 * 1024

// getPreference: GET /api/v1/me/preferences/{key}
// Returns {key, value}; value is null when nothing is stored yet.
func (h *Handlers) getPreference(w http.ResponseWriter, r *http.Request) {
	p := middleware.Principal(r)
	key := r.PathValue("key")
	if p.UserID == nil {
		httpserver.WriteError(w, http.StatusForbidden, "preferences are per-user; service tokens have none")
		return
	}
	if !prefKeyRe.MatchString(key) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid preference key")
		return
	}
	var raw []byte
	err := h.PGPool.QueryRow(r.Context(), `
		SELECT value FROM user_preferences
		WHERE user_id = $1 AND organization_id = $2 AND key = $3`,
		*p.UserID, p.OrgID, key).Scan(&raw)
	if err != nil {
		// No row (or any read hiccup) reads as "no preference" — the
		// frontend falls back to defaults either way.
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"key": key, "value": nil})
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"key": key, "value": json.RawMessage(raw)})
}

// putPreference: PUT /api/v1/me/preferences/{key} with {"value": <json>}
// Upserts; a null value deletes the stored preference.
func (h *Handlers) putPreference(w http.ResponseWriter, r *http.Request) {
	p := middleware.Principal(r)
	key := r.PathValue("key")
	if p.UserID == nil {
		httpserver.WriteError(w, http.StatusForbidden, "preferences are per-user; service tokens have none")
		return
	}
	if !prefKeyRe.MatchString(key) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid preference key")
		return
	}
	var body struct {
		Value json.RawMessage `json:"value"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, prefMaxBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body (or value too large)")
		return
	}
	if len(body.Value) == 0 || string(body.Value) == "null" {
		if _, err := h.PGPool.Exec(r.Context(), `
			DELETE FROM user_preferences
			WHERE user_id = $1 AND organization_id = $2 AND key = $3`,
			*p.UserID, p.OrgID, key); err != nil {
			h.Logger.Error("delete preference failed", "err", err, "key", key)
			httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !json.Valid(body.Value) {
		httpserver.WriteError(w, http.StatusBadRequest, "value must be valid JSON")
		return
	}
	if _, err := h.PGPool.Exec(r.Context(), `
		INSERT INTO user_preferences (user_id, organization_id, key, value, updated_at)
		VALUES ($1, $2, $3, $4::jsonb, now())
		ON CONFLICT (user_id, organization_id, key)
		DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		*p.UserID, p.OrgID, key, string(body.Value)); err != nil {
		h.Logger.Error("save preference failed", "err", err, "key", key)
		httpserver.WriteError(w, http.StatusInternalServerError, "save failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
