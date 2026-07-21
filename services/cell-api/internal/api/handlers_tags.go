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
	"github.com/sluicio/sluicio-app/services/cell-api/internal/tags"
)

// Request / response shapes for tags.

type createTagRequest struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type updateTagRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// listTags: GET /api/v1/tags[?include=usage]
//
// Returns every tag in the org, ordered by name. With ?include=usage
// each row carries integration_count and service_count, used by the
// management page so delete confirmations can name what cascades.
// The bare shape stays the default so the picker stays cheap.
func (h *Handlers) listTags(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("include") == "usage" {
		rows, err := h.Tags.ListWithUsage(r.Context(), middleware.OrgID(r))
		if err != nil {
			h.Logger.Error("list tags with usage failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
			return
		}
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"tags": rows})
		return
	}
	rows, err := h.Tags.List(r.Context(), middleware.OrgID(r))
	if err != nil {
		h.Logger.Error("list tags failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"tags": rows})
}

// getTag: GET /api/v1/tags/{id}
func (h *Handlers) getTag(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid tag id")
		return
	}
	t, err := h.Tags.Get(r.Context(), middleware.OrgID(r), id)
	if err != nil {
		if errors.Is(err, tags.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "tag not found")
			return
		}
		h.Logger.Error("get tag failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, t)
}

// createTag: POST /api/v1/tags
func (h *Handlers) createTag(w http.ResponseWriter, r *http.Request) {
	var req createTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	in := tags.Tag{
		OrganizationID: middleware.OrgID(r),
		Slug:           strings.TrimSpace(strings.ToLower(req.Slug)),
		Name:           strings.TrimSpace(req.Name),
		Color:          tags.NormalizeColor(req.Color),
	}
	if err := in.Validate(); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := h.Tags.Create(r.Context(), in)
	if err != nil {
		if errors.Is(err, tags.ErrSlugConflict) {
			httpserver.WriteError(w, http.StatusConflict, "a tag with this slug already exists")
			return
		}
		h.Logger.Error("create tag failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.recordAudit(r, "tag.created", "tag", created.ID.String(), map[string]any{"slug": created.Slug, "name": created.Name})
	httpserver.WriteJSON(w, http.StatusCreated, created)
}

// updateTag: PATCH /api/v1/tags/{id}
//
// Slug is intentionally immutable — saved searches and chips that
// reference a slug stay valid across renames.
func (h *Handlers) updateTag(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid tag id")
		return
	}
	var req updateTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	color := tags.NormalizeColor(req.Color)
	// Reuse the same validation by building a stand-in struct with the
	// existing slug (any non-empty slug passes the slug check; we keep
	// the real one in the DB).
	probe := tags.Tag{Slug: "x", Name: name, Color: color}
	if err := probe.Validate(); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := h.Tags.Update(r.Context(), middleware.OrgID(r), id, name, color)
	if err != nil {
		if errors.Is(err, tags.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "tag not found")
			return
		}
		h.Logger.Error("update tag failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	h.recordAudit(r, "tag.updated", "tag", id.String(), map[string]any{"name": updated.Name})
	httpserver.WriteJSON(w, http.StatusOK, updated)
}

// deleteTag: DELETE /api/v1/tags/{id}
func (h *Handlers) deleteTag(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid tag id")
		return
	}
	if err := h.Tags.Delete(r.Context(), middleware.OrgID(r), id); err != nil {
		if errors.Is(err, tags.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "tag not found")
			return
		}
		h.Logger.Error("delete tag failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	h.recordAudit(r, "tag.deleted", "tag", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// integration links

// listIntegrationTags: GET /api/v1/integrations/{id}/tags
func (h *Handlers) listIntegrationTags(w http.ResponseWriter, r *http.Request) {
	integID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return
	}
	if _, ok := h.gateIntegrationMembers(w, r, integID); !ok {
		return
	}
	rows, err := h.Tags.ListForIntegration(r.Context(), middleware.OrgID(r), integID)
	if err != nil {
		h.Logger.Error("list integration tags failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"tags": rows})
}

// attachIntegrationTag: POST /api/v1/integrations/{id}/tags/{tagId}
//
// Idempotent: re-attaching is a no-op so the UI can fire the same
// request on every chip add without having to track local state.
func (h *Handlers) attachIntegrationTag(w http.ResponseWriter, r *http.Request) {
	integID, tagID, ok := parseIntegTag(w, r)
	if !ok {
		return
	}
	if err := h.Tags.AttachToIntegration(r.Context(), middleware.OrgID(r), integID, tagID); err != nil {
		if errors.Is(err, tags.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "tag or integration not found")
			return
		}
		h.Logger.Error("attach integration tag failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "attach failed")
		return
	}
	h.recordAudit(r, "tag.attached", "tag", tagID.String(), map[string]any{"target_kind": "integration", "target_id": integID.String()})
	w.WriteHeader(http.StatusNoContent)
}

// detachIntegrationTag: DELETE /api/v1/integrations/{id}/tags/{tagId}
func (h *Handlers) detachIntegrationTag(w http.ResponseWriter, r *http.Request) {
	integID, tagID, ok := parseIntegTag(w, r)
	if !ok {
		return
	}
	if err := h.Tags.DetachFromIntegration(r.Context(), middleware.OrgID(r), integID, tagID); err != nil {
		if errors.Is(err, tags.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "tag link not found")
			return
		}
		h.Logger.Error("detach integration tag failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "detach failed")
		return
	}
	h.recordAudit(r, "tag.detached", "tag", tagID.String(), map[string]any{"target_kind": "integration", "target_id": integID.String()})
	w.WriteHeader(http.StatusNoContent)
}

// service links

// listServiceTags: GET /api/v1/services/{name}/tags
func (h *Handlers) listServiceTags(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	rows, err := h.Tags.ListForService(r.Context(), middleware.OrgID(r), name)
	if err != nil {
		h.Logger.Error("list service tags failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "query failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"tags": rows})
}

// attachServiceTag: POST /api/v1/services/{name}/tags/{tagId}
func (h *Handlers) attachServiceTag(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	tagID, err := uuid.Parse(r.PathValue("tagId"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid tag id")
		return
	}
	if err := h.Tags.AttachToService(r.Context(), middleware.OrgID(r), name, tagID); err != nil {
		if errors.Is(err, tags.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "tag not found")
			return
		}
		if tags.IsValidationError(err) {
			httpserver.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.Logger.Error("attach service tag failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "attach failed")
		return
	}
	h.recordAudit(r, "tag.attached", "tag", tagID.String(), map[string]any{"target_kind": "service", "target_id": name})
	w.WriteHeader(http.StatusNoContent)
}

// detachServiceTag: DELETE /api/v1/services/{name}/tags/{tagId}
func (h *Handlers) detachServiceTag(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "service name is required")
		return
	}
	tagID, err := uuid.Parse(r.PathValue("tagId"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid tag id")
		return
	}
	if err := h.Tags.DetachFromService(r.Context(), middleware.OrgID(r), name, tagID); err != nil {
		if errors.Is(err, tags.ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "tag link not found")
			return
		}
		h.Logger.Error("detach service tag failed", "err", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "detach failed")
		return
	}
	h.recordAudit(r, "tag.detached", "tag", tagID.String(), map[string]any{"target_kind": "service", "target_id": name})
	w.WriteHeader(http.StatusNoContent)
}

// parseIntegTag pulls and validates the integration_id and tag_id
// path parameters. On error it has already written the response.
func parseIntegTag(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	integID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid integration id")
		return uuid.Nil, uuid.Nil, false
	}
	tagID, err := uuid.Parse(r.PathValue("tagId"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid tag id")
		return uuid.Nil, uuid.Nil, false
	}
	return integID, tagID, true
}
