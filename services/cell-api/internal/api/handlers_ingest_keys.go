// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/api/middleware"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/ingestkeys"
)

// HTTP surface for per-org OTLP ingest keys:
//
//   GET    /api/v1/ingest-keys        — list this org's live keys
//   POST   /api/v1/ingest-keys        — mint a key (full value returned ONCE)
//   DELETE /api/v1/ingest-keys/{id}   — revoke
//
// Mutations are gated to org admin (see handlers.go) — a leaked ingest
// key lets anyone write telemetry as the org.

type ingestKeyResponse struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at,omitempty"`
}

func toIngestKeyResponse(k ingestkeys.Key) ingestKeyResponse {
	r := ingestKeyResponse{
		ID:        k.ID.String(),
		Name:      k.Name,
		Prefix:    k.Prefix,
		CreatedAt: k.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if k.LastUsedAt != nil {
		s := k.LastUsedAt.UTC().Format("2006-01-02T15:04:05Z")
		r.LastUsedAt = &s
	}
	return r
}

// listIngestKeys: GET /api/v1/ingest-keys
func (h *Handlers) listIngestKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.IngestKeys.List(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list ingest keys failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]ingestKeyResponse, 0, len(keys))
	for _, k := range keys {
		out = append(out, toIngestKeyResponse(k))
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"keys": out})
}

type createIngestKeyRequest struct {
	Name string `json:"name"`
}

// createIngestKey: POST /api/v1/ingest-keys — returns the full key once.
func (h *Handlers) createIngestKey(w http.ResponseWriter, r *http.Request) {
	var req createIngestKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	var createdBy *uuid.UUID
	if p, ok := middleware.PrincipalFromContext(r.Context()); ok {
		createdBy = p.UserID // nil for service-account principals
	}
	full, key, err := h.IngestKeys.Create(r.Context(), middleware.OrgID(r), req.Name, createdBy)
	if err != nil {
		h.Logger.Error("create ingest key failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	// The full key is surfaced exactly once; only its hash is stored.
	resp := toIngestKeyResponse(key)
	h.recordAudit(r, "ingest_key.created", "ingest_key", key.ID.String(), map[string]any{"name": req.Name})
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{
		"key":  full,
		"meta": resp,
	})
}

// revokeIngestKey: DELETE /api/v1/ingest-keys/{id}
func (h *Handlers) revokeIngestKey(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	if err := h.IngestKeys.Revoke(r.Context(), middleware.OrgID(r), id); err != nil {
		if errors.Is(err, ingestkeys.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "key not found")
			return
		}
		h.Logger.Error("revoke ingest key failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "ingest_key.revoked", "ingest_key", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}
